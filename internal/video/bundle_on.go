//go:build mediamtx

package video

import _ "embed"

// Release builds carry MediaMTX inside ptz (cmd/mediamtx-bundle puts the
// pinned, checksum-verified build for the target here before go build
// -tags mediamtx). One program to install; updates bring the gateway too.

//go:embed bundle/mediamtx.bin
var bundled []byte

//go:embed bundle/LICENSE
var bundledLicense []byte
