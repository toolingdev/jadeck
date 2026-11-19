// Command mediamtx-bundle puts the pinned MediaMTX build for a target
// into internal/video/bundle so `go build -tags mediamtx` embeds it.
//
//	go run ./cmd/mediamtx-bundle linux amd64
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ptz/internal/video"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mediamtx-bundle GOOS GOARCH")
		os.Exit(2)
	}
	plat := os.Args[1] + "_" + os.Args[2]
	if os.Args[2] == "arm" {
		plat = os.Args[1] + "_armv7"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	bin, lic, err := video.Fetch(ctx, plat)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mediamtx-bundle:", err)
		os.Exit(1)
	}
	dir := filepath.Join("internal", "video", "bundle")
	if err := os.WriteFile(filepath.Join(dir, "mediamtx.bin"), bin, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), lic, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("bundled MediaMTX %s for %s (%d MB)\n", video.Version, plat, len(bin)>>20)
}
