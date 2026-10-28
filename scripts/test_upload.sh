#!/usr/bin/env bash
# ============================================================================
#  scripts/test_upload.sh — end-to-end multipart upload regression test.
#  Boots a throwaway server, logs in, uploads a real JPEG + MP4-ish file,
#  then verifies DB row, thumbnail generation and download endpoints.
#
#  Usage: ./scripts/test_upload.sh [path/to/immich-go-binary]
#  Log:   logs/build/upload-<UTC-ts>.log
# ============================================================================
set -u -o pipefail
cd "$(dirname "$0")/.." || exit 1

BIN="${1:-bin/immich-go}"
[ -x "$BIN" ] || { echo "binary not found: $BIN — run 'make build' first"; exit 1; }

mkdir -p logs/build
LOG="logs/build/upload-$(date -u +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG") 2>&1

PORT="${UP_PORT:-22831}"
DATA="$(mktemp -d /tmp/immich-upload.XXXXXX)"
SRVLOG="$DATA/server.log"
WORK="$DATA/work"; mkdir -p "$WORK"
PASS=0; FAIL=0
ok()   { echo "PASS  $*"; PASS=$((PASS+1)); }
bad()  { echo "FAIL  $*"; FAIL=$((FAIL+1)); }

cleanup() { [ -n "${SRV_PID:-}" ] && kill "$SRV_PID" 2>/dev/null; wait "${SRV_PID:-}" 2>/dev/null; rm -rf "$DATA"; }
trap cleanup EXIT

echo "== upload-test: starting $BIN on :$PORT (data=$DATA) =="
IMMICH_DATA_DIR="$DATA" IMMICH_PORT="$PORT" \
IMMICH_JWT_SECRET=test-secret IMMICH_ADMIN_EMAIL=admin@example.com IMMICH_ADMIN_PASSWORD=admin123 \
IMMICH_WORKER_INTERVAL=2s \
  "$BIN" >"$SRVLOG" 2>&1 &
SRV_PID=$!
for i in $(seq 1 20); do curl -s -m 1 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.5; done
BASE="http://127.0.0.1:$PORT"

TOKEN=$(curl -sS -m 10 -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"admin123"}' | sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p')
[ -n "$TOKEN" ] && ok "login" || { bad "login (no token)"; exit 1; }
AUTH=(-H "Authorization: Bearer $TOKEN")

# ---- craft a small valid JPEG (Go-generated if possible, else printf magic) --
JPEG="$WORK/photo.jpg"
if command -v go >/dev/null 2>&1; then
  cat > "$WORK/mkimg.go" <<'EOF'
package main
import ("image"; "image/color"; "image/jpeg"; "os")
func main(){
  im := image.NewRGBA(image.Rect(0,0,640,480))
  for y:=0;y<480;y++{ for x:=0;x<640;x++ { im.Set(x,y,color.RGBA{uint8(x),uint8(y),128,255}) } }
  f,_ := os.Create(os.Args[1]); jpeg.Encode(f,im,&jpeg.Options{Quality:90}); f.Close()
}
EOF
  (cd "$WORK" && go run mkimg.go photo.jpg) >/dev/null 2>&1
fi
[ -s "$JPEG" ] || printf '\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9' > "$JPEG"
VIDE="$WORK/clip.mp4"; head -c 200000 /dev/urandom > "$VIDE"

# ---- upload #1: JPEG --------------------------------------------------------
R=$(curl -sS -m 30 -w '\n%{http_code}' -X POST "$BASE/api/assets" "${AUTH[@]}" \
  -F "assetData=@$JPEG" -F "deviceAssetId=dev-001" -F "deviceId=smoke-device" \
  -F "fileCreatedAt=2026-10-28T10:00:00.000Z" -F "fileModifiedAt=2026-10-28T10:00:00.000Z" \
  -F "isFavorite=false" -F "duration=0")
CODE="${R##*$'\n'}"; BODY="${R%$'\n'*}"
if [ "$CODE" = "201" ] || [ "$CODE" = "200" ]; then ok "POST /api/assets JPEG (HTTP $CODE)"; else bad "upload JPEG (HTTP $CODE): ${BODY:0:400}"; fi
ASSET_ID=$(printf '%s' "$BODY" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "      asset id: ${ASSET_ID:-<none>}"

# ---- duplicate upload -> expect 409 or dedup handling -----------------------
R2=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' -X POST "$BASE/api/assets" "${AUTH[@]}" \
  -F "assetData=@$JPEG" -F "deviceAssetId=dev-001" -F "deviceId=smoke-device" \
  -F "fileCreatedAt=2026-10-28T10:00:00.000Z" -F "fileModifiedAt=2026-10-28T10:00:00.000Z")
case "$R2" in 200|201|409) ok "duplicate upload handled (HTTP $R2)";; *) bad "duplicate upload (HTTP $R2)";; esac

# ---- upload #2: video ---------------------------------------------------------
RV=$(curl -sS -m 30 -w '\n%{http_code}' -X POST "$BASE/api/assets" "${AUTH[@]}" \
  -F "assetData=@$VIDE;type=video/mp4" -F "deviceAssetId=dev-002" -F "deviceId=smoke-device" \
  -F "fileCreatedAt=2026-10-28T11:00:00.000Z" -F "fileModifiedAt=2026-10-28T11:00:00.000Z" \
  -F "duration=0:00:05")
VCODE="${RV##*$'\n'}"
[ "$VCODE" = "201" ] || [ "$VCODE" = "200" ] && ok "POST /api/assets MP4 (HTTP $VCODE)" || bad "upload MP4 (HTTP $VCODE)"

# ---- retrieval endpoints ------------------------------------------------------
if [ -n "$ASSET_ID" ]; then
  C=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' "$BASE/api/assets/$ASSET_ID" "${AUTH[@]}")
  [ "$C" = "200" ] && ok "GET /api/assets/:id" || bad "GET /api/assets/:id (HTTP $C)"
  C=$(curl -sS -m 10 -o "$WORK/thumb.jpg" -w '%{http_code}' "$BASE/api/assets/$ASSET_ID/thumbnail" "${AUTH[@]}")
  { [ "$C" = "200" ] && [ -s "$WORK/thumb.jpg" ]; } && ok "GET thumbnail (bytes served)" || bad "GET thumbnail (HTTP $C)"
  C=$(curl -sS -m 10 -o "$WORK/orig.jpg" -w '%{http_code}' "$BASE/api/assets/$ASSET_ID/original" "${AUTH[@]}")
  [ "$C" = "200" ] && ok "GET original" || bad "GET original (HTTP $C)"
  C=$(curl -sS -m 10 -H "Range: bytes=0-1023" -o /dev/null -w '%{http_code}' "$BASE/api/assets/$ASSET_ID/original" "${AUTH[@]}")
  [ "$C" = "206" ] && ok "Range request -> 206 Partial Content" || bad "Range request (HTTP $C, want 206)"
fi

# ---- gallery list contains what we uploaded -----------------------------------
# Immich's GET /api/assets returns a bare JSON array (no totalCount envelope).
L=$(curl -sS -m 10 "$BASE/api/assets?take=10&skip=0" "${AUTH[@]}")
if printf '%s' "$L" | grep -q '"deviceAssetId":"dev-001"'; then
  ok "GET /api/assets listing returns uploaded asset"
else
  bad "listing shape: ${L:0:300}"
fi

echo
echo "== upload-test summary: PASS=$PASS FAIL=$FAIL =="
[ "$FAIL" -eq 0 ] || { echo "-- server log tail --"; tail -n 40 "$SRVLOG"; }
echo "Log file: $LOG"
[ "$FAIL" -eq 0 ]
