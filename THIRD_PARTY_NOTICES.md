# Third-party software shipped in release builds

## MediaMTX

Release builds of `ptz` include MediaMTX (https://github.com/bluenviron/mediamtx),
the stream gateway behind live video. The exact version is pinned in
`internal/video/video.go`, and every build is checked against a pinned
SHA-256 before it is bundled.

License: MIT. The full license text ships with the gateway. The app
writes it to `data/video/bin/MEDIAMTX-LICENSE.txt` next to the program.
