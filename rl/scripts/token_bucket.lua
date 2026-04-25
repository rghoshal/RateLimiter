--[[
  Atomic Token Bucket Rate Limiter
  ─────────────────────────────────
  Keys:
    KEYS[1]  - token bucket state key  (e.g. "rl:ip:1.2.3.4")
    KEYS[2]  - request log key         (e.g. "rl:log:ip:1.2.3.4")

  Args:
    ARGV[1]  - capacity       (max tokens)
    ARGV[2]  - refill_rate    (tokens per second)
    ARGV[3]  - requested      (tokens to consume, default 1)
    ARGV[4]  - now_ms         (current unix ms from caller)
    ARGV[5]  - window_ms      (sliding window size for burst guard)
    ARGV[6]  - burst_limit    (max requests in window)
    ARGV[7]  - ttl_seconds    (key expiry)

  Returns (array):
    [1]  allowed     (1=yes, 0=no)
    [2]  tokens_left (remaining after this request)
    [3]  retry_after_ms (0 if allowed)
    [4]  burst_remaining (requests left in window)
    [5]  reset_at_ms (when bucket will be full again)
]]

local key        = KEYS[1]
local log_key    = KEYS[2]
local capacity   = tonumber(ARGV[1])
local rate       = tonumber(ARGV[2])   -- tokens/sec
local requested  = tonumber(ARGV[3]) or 1
local now_ms     = tonumber(ARGV[4])
local window_ms  = tonumber(ARGV[5])
local burst_max  = tonumber(ARGV[6])
local ttl        = tonumber(ARGV[7])

-- ── 1. Load or initialise bucket state ───────────────────────────────────────
local data = redis.call("HMGET", key, "tokens", "last_refill_ms")
local tokens      = tonumber(data[1]) or capacity
local last_refill = tonumber(data[2]) or now_ms

-- ── 2. Refill based on elapsed time ──────────────────────────────────────────
local elapsed_ms   = math.max(0, now_ms - last_refill)
local elapsed_sec  = elapsed_ms / 1000.0
local new_tokens   = math.min(capacity, tokens + (elapsed_sec * rate))

-- ── 3. Sliding-window burst guard ────────────────────────────────────────────
local window_start = now_ms - window_ms
redis.call("ZREMRANGEBYSCORE", log_key, "-inf", window_start)
local burst_count = tonumber(redis.call("ZCARD", log_key)) or 0
local burst_remaining = math.max(0, burst_max - burst_count)
local burst_ok = (burst_count < burst_max)

-- ── 4. Decision ──────────────────────────────────────────────────────────────
local allowed = 0
local tokens_after = new_tokens
local retry_after_ms = 0

if new_tokens >= requested and burst_ok then
    allowed = 1
    tokens_after = new_tokens - requested

    -- Persist updated state
    redis.call("HMSET", key,
        "tokens",         tokens_after,
        "last_refill_ms", now_ms
    )
    redis.call("EXPIRE", key, ttl)

    -- Record in sliding window log (score = timestamp)
    redis.call("ZADD", log_key, now_ms, now_ms .. ":" .. math.random(1e9))
    redis.call("EXPIRE", log_key, ttl)
else
    -- Compute retry_after
    if not burst_ok then
        -- Must wait until oldest entry exits window
        local oldest = redis.call("ZRANGE", log_key, 0, 0, "WITHSCORES")
        if oldest and oldest[2] then
            retry_after_ms = math.max(0, tonumber(oldest[2]) + window_ms - now_ms)
        else
            retry_after_ms = window_ms
        end
    else
        -- Need (requested - new_tokens) more tokens at rate tokens/sec
        local deficit = requested - new_tokens
        retry_after_ms = math.ceil((deficit / rate) * 1000)
    end
end

-- ── 5. Compute reset_at_ms ────────────────────────────────────────────────────
local tokens_needed = capacity - tokens_after
local reset_at_ms = now_ms + math.ceil((tokens_needed / rate) * 1000)

return {
    allowed,
    math.floor(tokens_after),
    retry_after_ms,
    burst_remaining,
    reset_at_ms
}
