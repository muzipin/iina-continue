# Continue

[简体中文](README.zh-CN.md)

## About

Continue lets you move local media playing in IINA to a browser on another device on the same local network, then return to IINA at the latest playback position. It is a local-first IINA plugin: no account, phone app, cloud relay, or runtime download is required.

## Features

- Continue playback between IINA and one other device without simultaneous playback.
- Play, pause, and seek with the browser's native media controls.
- Return to IINA at the latest confirmed position, even if the other device is still playing.
- Carry IINA's selected text subtitle tracks when the media path supports them.
- Choose Auto, Original, 1080p, or 720p quality; compatible files are served directly and other supported files use a bundled minimal FFmpeg build.
- Protect each temporary connection with a cryptographically random URL and bind it to the first device that connects.

## Installation

Continue requires an Apple silicon Mac (Intel Macs are not currently supported), macOS 12 or later, IINA 1.4.0 or later, and a trusted IPv4 local network shared by both devices.

### Install from GitHub

1. Open IINA → Settings → Plugins.
2. Choose **Install from GitHub**.
3. Enter `muzipin/iina-continue` and confirm the installation.

### Install from a local package

1. Download `iina-continue-v<version>-arm64.iinaplgz` and `SHA256SUMS` from the matching GitHub Release.
2. Optionally verify the package in Terminal:

   ```sh
   shasum -a 256 -c SHA256SUMS
   ```

3. Open IINA → Settings → Plugins → Install Plugin, select the downloaded `.iinaplgz` file, and confirm the installation.

The package is self-contained and will not download software while Continue is running.

When IINA 1.5.0 is officially released, Continue will use its localized plugin metadata to follow IINA's language. In IINA 1.4.x, the plugin manager shows the English description, while the running interface follows the system/WebView language.

## Build and test

The build process writes intermediate files to `build/` in the current workspace and creates the installable plugin, the pinned FFmpeg source archive required for source redistribution, and `SHA256SUMS` in `dist/v<version>/`. Both directories are ignored by Git.

```sh
make test
make all
```

Building requires a compatible Apple silicon macOS environment, Go 1.25.x, Node.js 18 or later, the macOS command-line build tools, and an internet connection when the pinned FFmpeg source archive is not already cached. Newer Go release lines may raise the generated helper's minimum macOS version, so the build rejects helpers that do not target macOS 12.

`make all` runs the Go and JavaScript tests, builds a minimal arm64 LGPL FFmpeg configuration, packages the plugin, and verifies architecture, deployment target, signatures, dynamic dependencies, FFmpeg configuration, package contents, checksums, and the helper self-check.

For each release, update the user-facing `version` in `Info.json` and increment the integer `ghVersion` used by IINA's online update check.

Source layout:

- `Info.json`: plugin metadata and online update version
- `plugin/`: plugin runtime source, sidebar interface, and JavaScript tests
- `helper/`: local Go server, device player, and Go tests
- `scripts/`: build, packaging, and verification scripts
- `licenses/`: dependency source and license information

## Usage

1. Play a supported local file in IINA.
2. Open the Continue sidebar, choose a playback quality, and select **Start**.
3. Scan the QR code with the other device, then press Play on the browser page to continue playback.
4. Press Play in IINA to sync the playback position and return. Press Play on the browser page again to sync IINA's playback position and continue on the other device.
5. When finished, select **End** on either device to stop the Continue service immediately.

Only one player is active at a time. Returning to the Mac pauses the other device before IINA seeks to the latest confirmed position and resumes playback.

## Limitations

The table describes broad support boundaries, not a guarantee for every container, codec, subtitle, browser, or network combination.

| Area | v0.1.x scope |
| --- | --- |
| Mac | Apple silicon only; Intel Macs are not supported |
| Content | Standard local media files; network URLs, discs, DRM content, and playlists are not supported |
| Playback | One Mac and one browser device on the same trusted IPv4 local network |
| Media conversion | Compatible streams may be served or remuxed; supported incompatible streams may use hardware video or AAC audio conversion |
| Subtitles | Selected text subtitles are supported where conversion succeeds; image subtitles are not supported |
| Video range | No HDR-to-SDR conversion and no cloud relay |

Browser and router behavior varies. Test important files on the actual device before relying on Continue for uninterrupted playback.

## Privacy and security

Continue has no cloud service and makes no internet requests at runtime. Media is transferred only between the Mac and the first device that opens the temporary access URL. The helper listens for the local session, while privileged control requests are restricted to the Mac's loopback interface.

Treat the QR code and its URL as temporary credentials. Use Continue only on a trusted local network, do not share a live URL, and end the service when it is no longer needed. A single Continue service currently lasts for up to four hours and ends automatically when it expires.

Report vulnerabilities privately through the GitHub repository's security-advisory feature rather than a public issue. Include affected versions, impact, and minimal reproduction steps, but do not attach private media or a live access URL.

## Contributing

Keep changes within the current local-first scope and avoid runtime dependencies unless they are essential. Before opening a pull request:

1. Run `make test`.
2. Run `make all` on an Apple silicon Mac for release-related changes.
3. Describe user-visible changes and update the corresponding tests.
4. Confirm that no media, build output, credentials, live access URLs, or machine-specific paths are committed.

Bug reports should include the IINA and macOS versions, Mac model, media container and codec details, subtitle type, playback quality, and exact reproduction steps. Do not attach copyrighted media.

## Third-party software

Release packages include a minimal FFmpeg and ffprobe build under LGPL-2.1-or-later. Each release provides the matching FFmpeg source archive, and each plugin package includes the upstream source URL, checksum, exact build configuration, and LGPL text. See [`licenses/FFMPEG-SOURCE.txt`](licenses/FFMPEG-SOURCE.txt) and [`scripts/build-ffmpeg.sh`](scripts/build-ffmpeg.sh).

The sidebar includes QRCode.js under the MIT License. Its copyright and license notice is in [`plugin/ui/qrcode-LICENSE.txt`](plugin/ui/qrcode-LICENSE.txt).

## License

Copyright © 2026 MZZP. Original project code is licensed under the [GNU General Public License v3.0](LICENSE). Bundled third-party components remain under their respective licenses described above.
