#!/usr/bin/env bash
# ============================================================================
#  scripts/smoke.sh — boot the server in a throwaway dir and verify the
#  Immich handshake endpoints that the iOS app calls on first connect.
#
#  Usage: ./scripts/smoke.sh [path/to/immich-go-binary]
#  Log:   logs/build/smoke-<UTC-ts>.log
# ============================================================================
set -u -o pipefail
cd "$(dirname "$0")/.." || exit 1

BIN="${1:-bin/immich-go}"
[ -x "$BIN" ] || { echo "binary not found: $BIN — run 'make build' first"; exit 1; }

mkdir -p logs/build
LOG="logs/build/smoke-$(date -u +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG") 2>&1

PORT="${SMOKE_PORT:-22830}"
DATA="$(mktemp -d /tmp/immich-smoke.XXXXXX)"
SRVLOG="$DATA/server.log"
PASS=0; FAIL=0

check() { # $1=name $2=curl-url $3=expected-substring [$4=extra curl args...]
  local name="$1" url="$2" expect="$3"; shift 3
  local body code out
  out=$(curl -sS -m 15 -w '\n%{http_code}' "$@" "$url" 2>&1) || { code="000"; body="$out"; }
  code="${out##*$'\n'}"; body="${out%$'\n'*}"
  if [ "$code" = "${EXPECT_CODE:-200}" ] && printf '%s' "$body" | grep -q "$expect"; then
    echo "PASS  $name (HTTP $code)"; PASS=$((PASS+1))
  else
    echo "FAIL  $name (HTTP $code, expected '$expect')"; echo "      body: ${body:0:300}"; FAIL=$((FAIL+1))
  fi
  LAST_BODY="$body"
}

cleanup() { [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null; wait "${SRV_PID:-}" 2>/dev/null; rm -rf "$DATA"; }
trap cleanup EXIT

echo "== smoke: starting $BIN on :$PORT (data=$DATA) =="
IMMICH_DATA_DIR="$DATA" IMMICH_PORT="$PORT" \
IMMICH_JWT_SECRET=smoke-secret IMMICH_ADMIN_EMAIL=admin@example.com IMMICH_ADMIN_PASSWORD=admin123 \
  "$BIN" >"$SRVLOG" 2>&1 &
SRV_PID=$!

# wait for readiness (max 10s)
for i in $(seq 1 20); do
  curl -s -m 1 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break
  sleep 0.5
done

BASE="http://127.0.0.1:$PORT"
check "GET /api/health"                  "$BASE/api/health"                  '"status":"ok"'
check "GET /api/server-info/version"     "$BASE/api/server-info/version"     '"major":1'
check "GET /api/server-info/config"      "$BASE/api/server-info/config"      '"machineLearning"'

TOKEN=$(curl -sS -m 10 -X POST "$BASE/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"admin123"}' | \
  sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p')
if [ -n "$TOKEN" ]; then echo "PASS  POST /api/auth/login (token acquired)"; PASS=$((PASS+1));
else echo "FAIL  POST /api/auth/login (no token in response)"; FAIL=$((FAIL+1)); fi

check "GET /api/user-me (Bearer)"        "$BASE/api/user-me"                '"isAdmin":true' \
  -H "Authorization: Bearer $TOKEN"
# Immich returns a bare JSON array for this endpoint; an empty gallery is [].
check "GET /api/assets (authed list)"    "$BASE/api/assets?take=1"          '^\[\|^{' \
  -H "Authorization: Bearer $TOKEN"
EXPECT_CODE=401 check "GET /api/user-me without token -> 401" "$BASE/api/user-me" 'unauthorized\|Unauthorized\|401'

echo
echo "== smoke summary: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ] || { echo "-- server log tail --"; tail -n 30 "$SRVLOG"; }
echo "Log file: $LOG"
[ "$FAIL" -eq 0 ]
