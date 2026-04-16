package api_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// pickFreePort asks the kernel for an unused TCP port so the test can
// listen without fighting for a fixed one.
func pickFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestServeGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv := api.NewServer(api.Config{
		Memory:   memory.NewService(memory.Config{Store: db.Observations()}),
		Sessions: session.NewService(session.Config{Store: db.Sessions()}),
		Skills:   skills.NewService(skills.Config{Store: db.Skills()}),
		Touches:  db.Touches(),
		Prewarm: prewarm.NewService(prewarm.Config{
			Observations: db.Observations(),
			Sessions:     db.Sessions(),
			Skills:       db.Skills(),
			Touches:      db.Touches(),
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	addr := pickFreePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, addr) }()

	// Wait for the listener to bind.
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp == nil {
		cancel()
		<-done
		t.Fatal("server never came up")
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("healthz: %d", resp.StatusCode)
	}

	// Cancel should trigger graceful shutdown.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server didn't shut down within 3s")
	}
}
