// Command mockcam runs the mock PTZOptics camera standalone:
// VISCA on 127.0.0.1:15678, HTTP on 127.0.0.1:18081.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"

	"ptz/internal/mockcam"
)

func main() {
	v := flag.String("visca", "127.0.0.1:15678", "VISCA TCP listen address")
	h := flag.String("http", "127.0.0.1:18081", "HTTP listen address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	m := mockcam.New()
	va, ha, err := m.Serve(ctx, *v, *h)
	if err != nil {
		slog.Error("mock start", "err", err)
		os.Exit(1)
	}
	slog.Info("mock camera up", "visca", va, "http", ha)
	<-ctx.Done()
}
