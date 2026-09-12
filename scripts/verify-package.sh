#!/bin/zsh
set -euo pipefail
root=${0:a:h:h}
version=$(/usr/bin/python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$root/Info.json")
release="$root/dist/v${version}"
pkg=${1:-"$release/iina-continue-v${version}-arm64.iinaplgz"}
/usr/bin/find "$root" -name .DS_Store -delete
local_paths='/''Users/[^/[:space:]]+|/''home/[^/[:space:]]+|/''var/folders/'
source_files=("${(@f)$(find "$root" \( -path "$root/.git" -o -path "$root/build" -o -path "$root/dist" \) -prune -o -type f -print)}")
if grep -IEn "$local_paths" "$source_files[@]" >/dev/null; then echo "local path found in source tree" >&2; exit 1; fi
retired='continu''ity|hand''off'
if grep -IEinw "$retired" "$source_files[@]" >/dev/null; then echo "retired product name found in source tree" >&2; exit 1; fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
unzip -q "$pkg" -d "$tmp"
/usr/bin/python3 -m json.tool "$tmp/Info.json" >/dev/null
/usr/bin/python3 - "$tmp/Info.json" "$version" <<'PY'
import json, sys
info = json.load(open(sys.argv[1]))
assert info["version"] == sys.argv[2]
assert info["ghRepo"] == "muzipin/iina-continue"
assert type(info["ghVersion"]) is int and info["ghVersion"] > 0
assert info["identifier"] == "dev.mzzp.iina-continue"
assert info["permissions"] == ["file-system"]
assert info["name"] == "Continue"
assert info["author"]["name"] == "MZZP"
assert info["description"] == info["localized"]["en"]["description"]
assert info["description"] == "Continue what is playing in IINA on another device."
assert info["sidebarTab"]["name"] == "Continue"
assert info["localized"]["zh-Hans"]["name"] == "Continue"
assert info["localized"]["zh-Hans"]["sidebarTab"]["name"] == "Continue"
assert info["localized"]["zh-Hans"]["description"] == "在另一个设备继续 IINA 正在播放的内容"
PY
for f in bin/continue-helper bin/ffmpeg bin/ffprobe; do
  test -x "$tmp/$f"
  test "$(lipo -archs "$tmp/$f")" = arm64
  /usr/bin/vtool -show-build "$tmp/$f" | grep -q 'minos 12\.0'
  codesign --verify --strict "$tmp/$f"
  if otool -L "$tmp/$f" | tail -n +2 | grep -vE '^\s*/(usr/lib|System/Library)/' | grep -q .; then echo "unexpected dynamic dependency: $f" >&2; exit 1; fi
done
cmp "$root/Info.json" "$tmp/Info.json"
cmp "$root/plugin/main.js" "$tmp/main.js"
for f in continue.css continue.html continue.js qrcode-LICENSE.txt qrcode.min.js; do
  cmp "$root/plugin/ui/$f" "$tmp/ui/$f"
done
cmp "$root/build/helper/continue-helper" "$tmp/bin/continue-helper"
cmp "$root/build/ffmpeg/ffmpeg" "$tmp/bin/ffmpeg"
cmp "$root/build/ffmpeg/ffprobe" "$tmp/bin/ffprobe"
cmp "$root/build/ffmpeg/COPYING.LGPLv2.1" "$tmp/bin/COPYING.LGPLv2.1"
cmp "$root/build/ffmpeg/BUILD-CONFIGURATION.txt" "$tmp/bin/BUILD-CONFIGURATION.txt"
cmp "$root/licenses/FFMPEG-SOURCE.txt" "$tmp/bin/FFMPEG-SOURCE.txt"
test -f "$tmp/bin/COPYING.LGPLv2.1"
test -f "$tmp/LICENSE"
grep -q 'GNU GENERAL PUBLIC LICENSE' "$tmp/LICENSE"
test -f "$tmp/README.md"
grep -q 'Third-party software' "$tmp/README.md"
test -f "$tmp/bin/BUILD-CONFIGURATION.txt"
test -f "$tmp/bin/FFMPEG-SOURCE.txt"
test -f "$tmp/ui/qrcode-LICENSE.txt"
grep -q -- '--disable-network' "$tmp/bin/BUILD-CONFIGURATION.txt"
grep -q -- '--disable-encoders' "$tmp/bin/BUILD-CONFIGURATION.txt"
if "$tmp/bin/ffmpeg" -hide_banner -encoders 2>/dev/null | grep '^ V..... ' | grep -v '^ V..... =' | grep -v h264_videotoolbox | grep -q .; then echo "software video encoder found" >&2; exit 1; fi
if strings "$tmp/bin/continue-helper" "$tmp/bin/ffmpeg" "$tmp/bin/ffprobe" | grep -Fq "$root"; then echo "absolute build path found" >&2; exit 1; fi
"$tmp/bin/continue-helper" self-check | grep -q '"ok":true'
test -f "$release/ffmpeg-9.0.1.tar.xz"
test -f "$release/SHA256SUMS"
(cd "$release" && shasum -a 256 -c SHA256SUMS >/dev/null)
echo "package verified: $pkg"
