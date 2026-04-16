package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Server is the MCP stdio server. Wire it up with a set of services via
// NewServer, then call Serve with the stdin/stdout reader/writer pair.
type Server struct {
	name    string
	version string
	mem     *memory.Service
	sess    *session.Service
	skill   *skills.Service
	touches memory.TouchStore
	prewarm *prewarm.Service
	log     *slog.Logger

	storageSize func() (int64, error)

	mu              sync.Mutex // serialises stdout writes
	handlers        map[string]toolHandler
	toolOrder       []string
	toolDescriptors map[string]Tool
}

// Config bundles dependencies for NewServer.
type Config struct {
	Name        string
	Version     string
	Memory      *memory.Service
	Sessions    *session.Service
	Skills      *skills.Service
	Touches     memory.TouchStore
	Prewarm     *prewarm.Service
	Logger      *slog.Logger
	StorageSize func() (int64, error) // optional: powers storage_bytes in mnemos_stats
}

// NewServer builds an MCP server from service dependencies.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Name == "" {
		cfg.Name = "mnemos"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	s := &Server{
		name:        cfg.Name,
		version:     cfg.Version,
		mem:         cfg.Memory,
		sess:        cfg.Sessions,
		skill:       cfg.Skills,
		touches:     cfg.Touches,
		prewarm:     cfg.Prewarm,
		log:         cfg.Logger,
		storageSize: cfg.StorageSize,
	}
	s.registerTools()
	return s
}

// Serve runs the JSON-RPC message loop until r returns EOF. Messages are
// newline-delimited JSON on r; responses are written to w. Notifications
// (no ID) produce no response.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024) // up to 8 MiB per message

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := s.dispatch(ctx, line)
		if resp == nil {
			continue
		}
		if err := s.write(w, resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}

func (s *Server) write(w io.Writer, resp *rpcResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func (s *Server) dispatch(ctx context.Context, raw []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errorResponse(nil, errParse, "parse error", err.Error())
	}
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, errInvalidRequest, "jsonrpc must be 2.0", nil)
	}

	// Notifications (no ID) must never get a response.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return successResponse(req.ID, initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: serverCapabilities{
				Tools:     &toolsCapability{},
				Resources: &resourcesCapability{},
			},
			ServerInfo: serverInfo{Name: s.name, Version: s.version},
		})
	case "notifications/initialized", "initialized":
		return nil
	case "ping":
		return successResponse(req.ID, struct{}{})
	case "tools/list":
		return successResponse(req.ID, toolsListResult{Tools: s.toolList()})
	case "tools/call":
		return s.handleToolCall(ctx, req)
	case "resources/list":
		return successResponse(req.ID, resourcesListResult{Resources: s.resourceList()})
	case "resources/read":
		return s.handleResourceRead(ctx, req)
	default:
		if isNotification {
			return nil
		}
		return errorResponse(req.ID, errMethodNotFound, "method not found: "+req.Method, nil)
	}
}

func successResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, msg string, data any) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg, Data: data},
	}
}
