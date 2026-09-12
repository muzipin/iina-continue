#!/bin/zsh
set -euo pipefail
root=${0:a:h:h}
stage="$root/build/stage/iina-continue"
version=$(/usr/bin/python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$root/Info.json")
release="$root/dist/v${version}"
package_name="iina-continue-v${version}-arm64.iinaplgz"
ffmpeg_name="ffmpeg-9.0.1.tar.xz"
package="$release/$package_name"
ffmpeg_source="$root/build/$ffmpeg_name"
ffmpeg_sha256=cf38e0e28c7e5605942c4a77755349b0145804a397af37eb1fb4c77cb237f635
rm -rf "$stage"
mkdir -p "$stage/bin" "$stage/ui" "$release"
cp "$root/Info.json" "$root/plugin/main.js" "$stage/"
cp "$root/LICENSE" "$root/README.md" "$stage/"
cp "$root/plugin/ui/"* "$stage/ui/"
cp "$root/build/helper/continue-helper" "$root/build/ffmpeg/ffmpeg" "$root/build/ffmpeg/ffprobe" "$stage/bin/"
cp "$root/build/ffmpeg/COPYING.LGPLv2.1" "$root/build/ffmpeg/BUILD-CONFIGURATION.txt" "$stage/bin/"
cp "$root/licenses/FFMPEG-SOURCE.txt" "$stage/bin/"
chmod 755 "$stage/bin/continue-helper" "$stage/bin/ffmpeg" "$stage/bin/ffprobe"
/usr/bin/find "$stage" -name .DS_Store -delete
/usr/bin/find "$release" -maxdepth 1 -type f -name '*.iinaplgz' -delete
(cd "$stage" && /usr/bin/zip -q -r "$package" .)
actual=$(shasum -a 256 "$ffmpeg_source" | cut -d' ' -f1)
[[ "$actual" = "$ffmpeg_sha256" ]] || { echo "FFmpeg source checksum mismatch" >&2; exit 1; }
cp "$ffmpeg_source" "$release/$ffmpeg_name"
(cd "$release" && shasum -a 256 "$package_name" "$ffmpeg_name" > SHA256SUMS)
echo "$package"
