// Package mcp wraps the official Model Context Protocol Go SDK
// (github.com/modelcontextprotocol/go-sdk) with Mnemos-specific tool and
// resource registrations. The heavy lifting — JSON-RPC framing, schema
// inference, transport plumbing — lives in the SDK; this package owns
// the mapping between MCP messages and Mnemos services.
package mcp

import (
	"context"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Server is the Mnemos MCP server. It is a thin wrapper around the SDK
// server, holding the service dependencies our tool handlers close over.
type Server struct {
	sdk *mcpsdk.Server
	cfg Config
}

// Config bundles the dependencies a Server needs. Every field except
// Name, Version, and Logger is required.
type Config struct {
	Name        string
	Version     string
	Memory      *memory.Service
	Sessions    *session.Service
	Skills      *skills.Service
	Touches     memory.TouchStore
	Prewarm     *prewarm.Service
	Logger      *slog.Logger
	StorageSize func() (int64, error) // optional: reports storage_bytes in mnemos_stats
}

// NewServer constructs an MCP server with all Mnemos tools and resources
// registered. The server is ready to call Run/ServeStdio; no further
// registration is required.
func NewServer(cfg Config) *Server {
	if cfg.Name == "" {
		cfg.Name = "mnemos"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	sdk := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    cfg.Name,
		Version: cfg.Version,
	}, nil)

	s := &Server{sdk: sdk, cfg: cfg}
	s.registerTools()
	s.registerResources()
	return s
}

// ServeStdio runs the server over the stdio transport until ctx is
// cancelled or the peer disconnects.
func (s *Server) ServeStdio(ctx context.Context) error {
	return s.sdk.Run(ctx, &mcpsdk.StdioTransport{})
}

// SDK returns the underlying SDK server. Useful for tests that need to
// wire an in-memory transport pair.
func (s *Server) SDK() *mcpsdk.Server { return s.sdk }
