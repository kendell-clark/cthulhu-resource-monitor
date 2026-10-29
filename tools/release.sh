#!/usr/bin/env bash
# Build and publish a Cthulhu Resource Monitor release from a persistent Debian container.
set -Eeuo pipefail

usage() { echo "Usage: $0 VERSION [--dry-run]   (example: $0 0.1.2 --dry-run)" >&2; exit 2; }
[[ $# -ge 1 && $# -le 2 ]] || usage
VERSION=$1
[[ $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || usage
DRY_RUN=false
if [[ $# == 2 ]]; then [[ $2 == --dry-run ]] || usage; DRY_RUN=true; fi

CONTAINER=${RESOURCE_MONITOR_CONTAINER:-resource-monitor-builder}
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
SDK=${CTHULHU_SOURCE_DIR:-"$(dirname "$ROOT")/cthulhu"}
TAG="resource-monitor-v$VERSION"
ASSET="resource-monitor-$VERSION-linux-amd64.tar.gz"
URL="https://github.com/kendell-clark/cthulhu-resource-monitor/releases/download/$TAG/$ASSET"
DIST="$ROOT/dist"
STAGE="$DIST/staging-$VERSION"
BIN="$DIST/resource-monitor-$VERSION-linux-amd64"
ARCHIVE="$DIST/$ASSET"

fail() { echo "ERROR: $*" >&2; exit 1; }
for cmd in podman git gh python3 tar sha256sum; do command -v "$cmd" >/dev/null || fail "Missing command: $cmd"; done
[[ -f "$ROOT/go.mod" && -f "$ROOT/resource-monitor/tentacle.toml" && -f "$ROOT/catalog.toml" ]] || fail "Unexpected repository layout"
[[ -f "$SDK/go.mod" ]] || fail "Cthulhu SDK not found at $SDK (set CTHULHU_SOURCE_DIR)"
[[ -f "$ROOT/resource-monitor/README.md" && -f "$ROOT/LICENSE" ]] || fail "Missing package README or LICENSE"
[[ -z $(git -C "$ROOT" status --porcelain) ]] || fail "Commit or stash changes before releasing (git tree must be clean)"
[[ $(git -C "$ROOT" branch --show-current) != '' ]] || fail "Detached HEAD"
[[ -z $(git -C "$ROOT" tag -l "$TAG") ]] || fail "Tag already exists: $TAG"
[[ ! -e "$ARCHIVE" ]] || fail "Archive already exists: $ARCHIVE"
[[ ! -e "$STAGE" ]] || fail "Staging directory already exists: $STAGE"
[[ $(podman inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null || true) == true ]] || fail "Container '$CONTAINER' must be running (podman start $CONTAINER)"
if [[ $DRY_RUN == false ]]; then gh auth status >/dev/null || fail "Authenticate GitHub CLI: gh auth login"; fi

# Copy fresh sources into the persistent container, preserving go.mod's ../cthulhu replacement.
echo "Copying current source and Cthulhu SDK into $CONTAINER..."
podman exec "$CONTAINER" sh -c 'rm -rf /opt/cthulhu-resource-monitor /opt/cthulhu && mkdir -p /opt/cthulhu-resource-monitor /opt/cthulhu'
podman cp "$ROOT/." "$CONTAINER:/opt/cthulhu-resource-monitor"
podman cp "$SDK/." "$CONTAINER:/opt/cthulhu"

echo "Building and testing on Debian..."
podman exec -e GOROOT=/opt/golang -e PATH=/opt/golang/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin -e CGO_ENABLED=1 "$CONTAINER" sh -ec '
  cd /opt/cthulhu-resource-monitor
  go test -buildvcs=false ./resource-monitor/src
  go build -buildvcs=false -trimpath -ldflags="-s -w" -o /opt/resource-monitor-release ./resource-monitor/src
'
mkdir -p "$DIST"
podman cp "$CONTAINER:/opt/resource-monitor-release" "$BIN"
chmod 755 "$BIN"

# Check actual ELF symbol versions; a Debian-built binary must not need a newer glibc.
if command -v readelf >/dev/null; then
  if readelf --version-info "$BIN" | grep -Eq 'Name: GLIBC_2\.(3[7-9]|[4-9][0-9]|[1-9][0-9][0-9])([[:space:]]|$)'; then
    fail "Binary requires glibc newer than 2.36"
  fi
else
  echo "WARNING: readelf unavailable; cannot validate glibc symbol requirements" >&2
fi

# Create package with versioned manifest; only modify tracked metadata after build succeeds.
mkdir -p "$STAGE/bin"
cp "$ROOT/resource-monitor/tentacle.toml" "$STAGE/tentacle.toml"
cp "$ROOT/resource-monitor/README.md" "$STAGE/README.md"
cp "$ROOT/LICENSE" "$STAGE/LICENSE"
cp "$BIN" "$STAGE/bin/resource-monitor"
python3 - "$STAGE/tentacle.toml" "$VERSION" <<'PY'
import pathlib, re, sys
p = pathlib.Path(sys.argv[1]); s = p.read_text()
s, n = re.subn(r'(?m)^version\s*=\s*"[^"]+"\s*$', f'version = "{sys.argv[2]}"', s)
if n != 1: raise SystemExit('Expected exactly one manifest version field')
p.write_text(s)
PY
( cd "$STAGE" && tar -czf "$ARCHIVE" tentacle.toml README.md LICENSE bin/resource-monitor )
tar -tzf "$ARCHIVE"
SHA=$(sha256sum "$ARCHIVE" | awk '{print $1}')
echo "Archive: $ARCHIVE"
echo "SHA256:  $SHA"

if [[ $DRY_RUN == true ]]; then
  echo "DRY RUN: Built and packaged successfully. No tracked files changed; nothing published."
  exit 0
fi

# Update manifest and catalog in place, preserving other catalog fields.
python3 - "$ROOT/resource-monitor/tentacle.toml" "$ROOT/catalog.toml" "$VERSION" "$URL" "$SHA" <<'PY'
import pathlib, re, sys
manifest, catalog = map(pathlib.Path, sys.argv[1:3]); version, url, sha = sys.argv[3:]
s = manifest.read_text()
s, n = re.subn(r'(?m)^version\s*=\s*"[^"]+"\s*$', f'version = "{version}"', s)
if n != 1: raise SystemExit('Expected exactly one manifest version field')
manifest.write_text(s)
s = catalog.read_text()
for field, value in [('url', url), ('sha256', sha)]:
    s, n = re.subn(r'(?m)^' + field + r'\s*=\s*"[^"]+"\s*$', lambda m: f'{field} = "{value}"', s)
    if n != 1: raise SystemExit(f'Expected exactly one {field} field in catalog; found {n}')
catalog.write_text(s)
PY

# Only these two tracked files are expected to change.
git -C "$ROOT" add -- resource-monitor/tentacle.toml catalog.toml
git -C "$ROOT" diff --cached --check
git -C "$ROOT" commit -m "Release Resource Monitor v$VERSION"
git -C "$ROOT" tag "$TAG"
BRANCH=$(git -C "$ROOT" branch --show-current)
echo "Pushing commit and tag..."
git -C "$ROOT" push origin "$BRANCH"
git -C "$ROOT" push origin "$TAG"
echo "Creating GitHub release and uploading archive..."
gh release create "$TAG" "$ARCHIVE" --repo kendell-clark/cthulhu-resource-monitor --title "Resource Monitor v$VERSION" --notes "Resource Monitor v$VERSION for Linux amd64. Built on Debian 12 for broader glibc compatibility."
echo "Published $TAG"
echo "$URL"
