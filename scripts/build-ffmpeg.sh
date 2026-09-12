#!/bin/zsh
set -euo pipefail
root=${0:a:h:h}
out="$root/build/ffmpeg"
src=${FFMPEG_SRC:-"$root/build/ffmpeg-src"}
tarball="$root/build/ffmpeg-9.0.1.tar.xz"
mkdir -p "$out" "$root/build"
if [[ ! -f "$src/RELEASE" ]]; then
  [[ -f "$tarball" ]] || curl -fL https://ffmpeg.org/releases/ffmpeg-9.0.1.tar.xz -o "$tarball.part"
  [[ -f "$tarball" ]] || mv "$tarball.part" "$tarball"
  actual=$(shasum -a 256 "$tarball" | cut -d' ' -f1)
  [[ "$actual" = cf38e0e28c7e5605942c4a77755349b0145804a397af37eb1fb4c77cb237f635 ]] || { echo "FFmpeg source checksum mismatch" >&2; exit 1; }
  rm -rf "$src"
  mkdir -p "$src"
  tar -xJf "$tarball" -C "$src" --strip-components=1
fi
cd "$src"
make distclean >/dev/null 2>&1 || true
./configure --prefix=/opt/iina-continue --arch=arm64 --target-os=darwin --disable-autodetect --disable-doc --disable-debug --disable-network --disable-programs --enable-ffmpeg --enable-ffprobe --enable-videotoolbox --enable-audiotoolbox --enable-zlib --disable-encoders --enable-encoder=aac --enable-encoder=h264_videotoolbox --enable-encoder=webvtt --disable-muxers --enable-muxer=hls --enable-muxer=mpegts --enable-muxer=webvtt --extra-cflags='-mmacosx-version-min=12.0' --extra-ldflags='-mmacosx-version-min=12.0'
make -j"$(sysctl -n hw.ncpu)" ffmpeg ffprobe
cp ffmpeg ffprobe "$out/"
codesign --force --sign - "$out/ffmpeg" "$out/ffprobe"
cp COPYING.LGPLv2.1 "$out/"
./ffmpeg -buildconf > "$out/BUILD-CONFIGURATION.txt" 2>&1
