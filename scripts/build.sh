#!/usr/bin/env bash
# ============================================================================
#  scripts/build.sh — one-stop build with a persistent, timestamped log.
#
#  Usage:
#     ./scripts/build.sh              # native build (current machine)
#     ./scripts/build.sh all          # linux/amd64 + linux/arm64
#     ./scripts/build.sh amd64        # only x86_64
#     ./scripts/build.sh arm64        # only ODROID-C2
#     ./scripts/build.sh verify       # deps+vet+all archs (full gate)
#
#  Every run writes: logs/build/build-<mode>-<UTC-timestamp>.log
#  The log path is printed at the end — send THAT file when reporting issues.
# ============================================================================
set -u -o pipefail

cd "$(dirname "$0")/.." || exit 1
MODE="${1:-native}"

mkdir -p bin logs/build
TS="$(date -u +%Y%m%d-%H%M%S)"
LOG="logs/build/build-${MODE}-${TS}.log"

VERSION="${VERSION:-1.100.0}"
LDFLAGS="-s -w -X main.version=${VERSION}"

# ---- tee everything to the log ---------------------------------------------
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n===== %s =====\n' "$*"; }

banner "ENVIRONMENT"
echo "date:    $(date -Is)"
echo "host:    $(uname -srm)"
echo "user:    $(whoami)@$(hostname 2>/dev/null || echo unknown)"
echo "pwd:     $(pwd)"
if command -v go >/dev/null 2>&1; then
  echo "go:      $(go version)"
  echo "GOFLAGS: $(go env GOOS)/$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED)"
else
  echo "go:      NOT FOUND in PATH — install Go >= 1.21 first:"
  echo "         https://go.dev/dl/  (e.g. wget the tarball, extract to /usr/local,"
  echo "         export PATH=\$PATH:/usr/local/go/bin)"
  echo "RESULT: FAIL (no toolchain)"
  exit 127
fi
[ -f go.mod ] || { echo "RESULT: FAIL (go.mod missing — run from repo root?)"; exit 1; }

banner "MODULES"
go mod download && go mod verify

banner "STATIC ANALYSIS"
go vet ./... || { echo "RESULT: FAIL (go vet)"; exit 1; }

build_one() { # $1=os $2=arch
  local os="$1" arch="$2" suffix=""
  [ "$os/$arch" = "$(go env GOOS)/$(go env GOARCH)" ] || suffix="-linux-${arch}"
  banner "BUILD ${os}/${arch} (version ${VERSION})"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "bin/immich-go${suffix}"          ./cmd/server    && echo "  ok immich-go"          || { echo "RESULT: FAIL (server ${os}/${arch})"; exit 1; }
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "bin/immich-migrate${suffix}"     ./cmd/migrate   && echo "  ok immich-migrate"     || { echo "RESULT: FAIL (migrate ${os}/${arch})"; exit 1; }
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "bin/immich-go-installer${suffix}" ./cmd/installer && echo "  ok immich-go-installer" || { echo "RESULT: FAIL (installer ${os}/${arch})"; exit 1; }
  command -v file >/dev/null && file "bin/immich-go${suffix}" || true
}

case "$MODE" in
  native)  eval build_one "$(go env GOOS)" "$(go env GOARCH)" ;;
  amd64)   build_one linux amd64 ;;
  arm64)   build_one linux arm64 ;;
  all)     build_one linux amd64; build_one linux arm64 ;;
  verify)  banner "TESTS"; go test ./... || { echo "RESULT: FAIL (tests)"; exit 1; }
           build_one linux amd64; build_one linux arm64 ;;
  *) echo "unknown mode: $MODE (use: native|amd64|arm64|all|verify)"; exit 2 ;;
esac

banner "ARTIFACTS"
ls -lh bin/ 2>/dev/null
echo "RESULT: SUCCESS"
banner "DONE"
echo "Log file: $LOG"
