#!/bin/zsh
set -euo pipefail
root=${0:a:h:h}
go_bin=${GO_BIN:-go}
mkdir -p "$root/build/helper"
output="$root/build/helper/continue-helper"
next="$root/build/helper/.continue-helper.next"
trap 'rm -f "$next"' EXIT
(cd "$root/helper" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$go_bin" build -trimpath -ldflags='-s -w' -o "$next" .)
if ! /usr/bin/vtool -show-build "$next" | /usr/bin/grep -q 'minos 12\.0'; then
  echo "helper does not target macOS 12; build with Go 1.25.x" >&2
  exit 1
fi
codesign --force --sign - "$next"
mv "$next" "$output"
