#!/usr/bin/env bash
# load_test.sh — hammers the API to demonstrate rate limiting behaviour.
# Requires: curl (standard), optional: vegeta (https://github.com/tsenart/vegeta)
#
# Usage:
#   ./load_test.sh              # curl-based burst test
#   ./load_test.sh --vegeta     # sustained load via vegeta (if installed)

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
TOTAL="${TOTAL:-100}"

# ── Colour helpers ────────────────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}=== Distributed Rate Limiter — Load Test ===${NC}"
echo "Target: $BASE_URL   Requests: $TOTAL"
echo ""

# ── 1. Curl burst test ────────────────────────────────────────────────────────
if [[ "${1:-}" != "--vegeta" ]]; then
    allowed=0
    denied=0
    retry_afters=()

    for i in $(seq 1 "$TOTAL"); do
        # Alternate between anonymous IP, authenticated user, and API-key requests
        case $((i % 3)) in
            0) headers=('-H' 'X-User-ID: user-alice') ;;
            1) headers=('-H' 'X-Api-Key: key-demo-123') ;;
            2) headers=() ;;
        esac

        response=$(curl -s -o /dev/null -w "%{http_code} %{time_total}" \
            "${headers[@]}" \
            -H "X-Real-IP: 203.0.113.$((i % 5))" \
            "$BASE_URL/api/data")

        code=$(echo "$response" | cut -d' ' -f1)
        ms=$(echo "$response" | cut -d' ' -f2 | awk '{printf "%.0f", $1*1000}')

        if [[ "$code" == "200" ]]; then
            ((allowed++))
            echo -e "  ${GREEN}✓ $i${NC}  HTTP $code  ${ms}ms"
        elif [[ "$code" == "429" ]]; then
            ((denied++))
            retry=$(curl -s -D - -o /dev/null \
                "${headers[@]}" \
                -H "X-Real-IP: 203.0.113.$((i % 5))" \
                "$BASE_URL/api/data" | grep -i 'retry-after:' | awk '{print $2}' | tr -d '\r' || echo "?")
            retry_afters+=("$retry")
            echo -e "  ${RED}✗ $i${NC}  HTTP $code  ${ms}ms  retry-after=${retry}s"
        else
            echo -e "  ${YELLOW}? $i${NC}  HTTP $code  ${ms}ms"
        fi
    done

    echo ""
    echo -e "${YELLOW}=== Results ===${NC}"
    echo -e "  ${GREEN}Allowed : $allowed${NC}"
    echo -e "  ${RED}Denied  : $denied${NC}"
    echo -e "  Denial rate: $(awk "BEGIN{printf \"%.1f\", $denied/$TOTAL*100}")%"
    if [[ ${#retry_afters[@]} -gt 0 ]]; then
        echo -e "  Sample Retry-After values: ${retry_afters[*]:0:5}"
    fi
    exit 0
fi

# ── 2. Vegeta sustained load test ─────────────────────────────────────────────
if ! command -v vegeta &>/dev/null; then
    echo "vegeta not found — install from https://github.com/tsenart/vegeta/releases"
    exit 1
fi

RATE="${RATE:-200}"          # req/s
DURATION="${DURATION:-30s}"

echo "Sustained load: ${RATE} req/s for ${DURATION}"
echo ""

echo "GET $BASE_URL/api/data
X-Real-IP: 203.0.113.1
X-User-ID: load-test-user" \
| vegeta attack -rate="$RATE" -duration="$DURATION" \
| vegeta report -type=text

echo ""
echo "Latency histogram:"
echo "GET $BASE_URL/api/data
X-Real-IP: 203.0.113.1" \
| vegeta attack -rate="$RATE" -duration="$DURATION" \
| vegeta report -type=hdrplot \
| head -20
