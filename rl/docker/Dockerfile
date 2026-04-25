# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Copy full source (go mod tidy needs all imports to generate go.sum)
COPY . .

# Generate go.sum and fetch all dependencies
RUN go mod tidy && go mod download

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /bin/server \
    ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM scratch

COPY --from=builder /bin/server /server

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/server", "healthcheck"]
ENTRYPOINT ["/server"]
