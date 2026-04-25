--[[
  Multi-Tier GCRA Rate Limiter (Atomic, O(N))
  ───────────────────────────────────────────
  KEYS:
    KEYS[1..N] - one key per tier

  ARGV (per tier, flattened):
    For each tier i:
      ARGV[(i-1)*3 + 1] = rate
      ARGV[(i-1)*3 + 2] = burst
      ARGV[(i-1)*3 + 3] = ttl_ms

  Returns:
    [1] allowed (1/0)
    [2] max_retry_after_ms
    [3] remaining_min
    [4] reset_at_max
]]

local num_keys = #KEYS

-- current time from Redis (consistent across cluster)
local t = redis.call("TIME")
local now = t[1] * 1000 + math.floor(t[2] / 1000)

-- store intermediate results
local tats = {}
local new_tats = {}
local allow_flags = {}
local retry_after_max = 0
local remaining_min = nil
local reset_at_max = 0

-- ─────────────────────────────────────────────
-- PASS 1: Evaluate all tiers (NO WRITES)
-- ─────────────────────────────────────────────
for i = 1, num_keys do
    local rate  = tonumber(ARGV[(i-1)*3 + 1])
    local burst = tonumber(ARGV[(i-1)*3 + 2])

    local interval = 1000.0 / rate
    local burst_offset = burst * interval

    local key = KEYS[i]
    local tat = tonumber(redis.call("GET", key))

    if not tat then
        tat = now
    end

    tats[i] = tat

    local allow_at = tat - burst_offset

    if now >= allow_at then
        allow_flags[i] = 1
        new_tats[i] = math.max(tat, now) + interval
    else
        allow_flags[i] = 0

        local retry = math.ceil(allow_at - now)
        if retry > retry_after_max then
            retry_after_max = retry
        end
    end

    -- compute remaining (approx)
    local remaining = math.max(0,
        math.floor((burst_offset - (tat - now)) / interval)
    )

    if remaining_min == nil or remaining < remaining_min then
        remaining_min = remaining
    end

    if tat > reset_at_max then
        reset_at_max = tat
    end
end

-- ─────────────────────────────────────────────
-- PASS 2: Decide + Commit (ONLY if all allowed)
-- ─────────────────────────────────────────────
local allowed = 1

for i = 1, num_keys do
    if allow_flags[i] == 0 then
        allowed = 0
        break
    end
end

if allowed == 1 then
    for i = 1, num_keys do
        local ttl = tonumber(ARGV[(i-1)*3 + 3])
        redis.call("SET", KEYS[i], new_tats[i], "PX", ttl)
    end
end

return {
    allowed,
    retry_after_max,
    remaining_min or 0,
    reset_at_max
}