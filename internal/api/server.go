// Package api exposes the Mnemos services over HTTP for multi-agent and
// remote setups. The transport is a thin adapter — every handler
// delegates to a service method. Business logic stays in internal/memory,
// internal/session, and internal/skills.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"golang.org/x/sync/errgroup"
)

// Server owns the HTTP handler tree.
type Server struct {
	mem         *memory.Service
	sess        *session.Service
	skill       *skills.Service
	touches     memory.TouchStore
	prewarm     *prewarm.Service
	log         *slog.Logger
	apiKey      string
	storageSize func() (int64, error)
}

// Config bundles dependencies. APIKey is optional; when empty, auth is off.
type Config struct {
	Memory      *memory.Service
	Sessions    *session.Service
	Skills      *skills.Service
	Touches     memory.TouchStore
	Prewarm     *prewarm.Service
	Logger      *slog.Logger
	APIKey      string
	StorageSize func() (int64, error)
}

// NewServer wires a Config into an HTTP server.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		mem:         cfg.Memory,
		sess:        cfg.Sessions,
		skill:       cfg.Skills,
		touches:     cfg.Touches,
		prewarm:     cfg.Prewarm,
		log:         cfg.Logger,
		apiKey:      cfg.APIKey,
		storageSize: cfg.StorageSize,
	}
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)

	// Observations ---------------------------------------------------
	mux.Handle("POST /v1/observations", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in memory.SaveInput) (int, *memory.SaveResult, error) {
			res, err := s.mem.Save(r.Context(), in)
			return http.StatusCreated, res, err
		}))
	mux.Handle("GET /v1/observations/{id}", pathOnly(
		http.StatusInternalServerError, memory.ErrNotFound,
		func(r *http.Request) (int, *memory.Observation, error) {
			o, err := s.mem.Get(r.Context(), r.PathValue("id"))
			return http.StatusOK, o, err
		}))
	mux.Handle("DELETE /v1/observations/{id}", pathOnly(
		http.StatusInternalServerError, memory.ErrNotFound,
		func(r *http.Request) (int, struct{}, error) {
			return http.StatusNoContent, struct{}{}, s.mem.Delete(r.Context(), r.PathValue("id"))
		}))

	// Search / context / link ----------------------------------------
	mux.Handle("POST /v1/search", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in memory.SearchInput) (int, map[string]any, error) {
			results, err := s.mem.Search(r.Context(), in)
			return http.StatusOK, map[string]any{"results": results}, err
		}))
	mux.Handle("POST /v1/context", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in memory.ContextInput) (int, *memory.ContextBlock, error) {
			block, err := s.mem.Context(r.Context(), in)
			return http.StatusOK, block, err
		}))
	mux.Handle("POST /v1/link", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in linkRequest) (int, map[string]any, error) {
			err := s.mem.Link(r.Context(), in.SourceID, in.TargetID, memory.LinkType(in.LinkType))
			return http.StatusOK, map[string]any{"ok": err == nil}, err
		}))

	// Sessions -------------------------------------------------------
	mux.Handle("POST /v1/sessions", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in session.OpenInput) (int, map[string]any, error) {
			sess, err := s.sess.Open(r.Context(), in)
			if err != nil {
				return 0, nil, err
			}
			out := map[string]any{"session_id": sess.ID, "started_at": sess.StartedAt}
			if s.prewarm != nil {
				if block, err := s.prewarm.Build(r.Context(), prewarm.Request{
					Mode:      prewarm.ModeSessionStart,
					AgentID:   in.AgentID,
					Project:   in.Project,
					Goal:      in.Goal,
					SessionID: sess.ID,
				}); err == nil && block != nil {
					out["prewarm"] = block
				}
			}
			return http.StatusCreated, out, nil
		}))
	mux.Handle("POST /v1/sessions/{id}/close", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in sessionCloseRequest) (int, map[string]any, error) {
			status := session.Status(in.Status)
			if status == "" {
				status = session.StatusOK
			}
			err := s.sess.Close(r.Context(), session.CloseInput{
				ID: r.PathValue("id"), Summary: in.Summary, Reflection: in.Reflection,
				Status: status, OutcomeTags: in.OutcomeTags,
			})
			return http.StatusOK, map[string]any{"ok": err == nil}, err
		}))

	// Skills ---------------------------------------------------------
	mux.Handle("POST /v1/skills", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in skills.SaveInput) (int, *skills.Skill, error) {
			sk, err := s.skill.Save(r.Context(), in)
			return http.StatusCreated, sk, err
		}))
	mux.Handle("POST /v1/skills/match", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in skills.MatchInput) (int, map[string]any, error) {
			matches, err := s.skill.Match(r.Context(), in)
			return http.StatusOK, map[string]any{"matches": matches}, err
		}))

	// Agent supercharge ---------------------------------------------
	mux.Handle("POST /v1/correct", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in correctRequest) (int, *memory.SaveResult, error) {
			content := fmt.Sprintf("**Tried:** %s\n\n**Wrong because:** %s\n\n**Fix:** %s",
				in.Tried, in.WrongBecause, in.Fix)
			res, err := s.mem.Save(r.Context(), memory.SaveInput{
				Title: in.Title, Content: content, Type: memory.TypeCorrection,
				Tags: in.Tags, Project: in.Project, AgentID: in.AgentID,
				SessionID: in.SessionID, Importance: 8,
			})
			return http.StatusCreated, res, err
		}))
	mux.Handle("POST /v1/convention", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in conventionRequest) (int, *memory.SaveResult, error) {
			content := in.Rule
			if in.Example != "" {
				content += "\n\nExample:\n" + in.Example
			}
			res, err := s.mem.Save(r.Context(), memory.SaveInput{
				Title: in.Title, Content: content, Type: memory.TypeConvention,
				Tags: in.Tags, Project: in.Project, AgentID: in.AgentID,
				Importance: 8, Rationale: in.Rationale,
			})
			return http.StatusCreated, res, err
		}))
	mux.Handle("POST /v1/touch", jsonIn(
		http.StatusBadRequest, nil,
		func(r *http.Request, in memory.TouchInput) (int, map[string]any, error) {
			if s.touches == nil {
				return 0, nil, errors.New("touch store not wired")
			}
			err := s.touches.Record(r.Context(), in)
			return http.StatusCreated, map[string]any{"ok": err == nil}, err
		}))

	// Stats ----------------------------------------------------------
	mux.Handle("GET /v1/stats", pathOnly(
		http.StatusInternalServerError, nil,
		func(r *http.Request) (int, map[string]any, error) {
			st, err := s.mem.Stats(r.Context())
			if err != nil {
				return 0, nil, err
			}
			out := map[string]any{
				"observations":      st.Observations,
				"live_observations": st.LiveObservations,
				"sessions":          st.Sessions,
				"skills":            st.Skills,
				"top_tags":          st.TopTags,
				"recent_sessions":   st.RecentSessions,
				"embedding":         map[string]any{"enabled": s.mem.HybridEnabled()},
			}
			if s.storageSize != nil {
				if size, err := s.storageSize(); err == nil {
					out["storage_bytes"] = size
				}
			}
			return http.StatusOK, out, nil
		}))

	return s.withAuth(s.withLogging(mux))
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Serve runs the HTTP server until ctx is cancelled. Uses errgroup to
// coordinate the listener goroutine and the context-driven shutdown.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		s.log.Info("http listen", "addr", addr)
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})
	return g.Wait()
}

// Request payloads ------------------------------------------------------

type linkRequest struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	LinkType string `json:"link_type"`
}

type sessionCloseRequest struct {
	Summary     string   `json:"summary"`
	Reflection  string   `json:"reflection"`
	Status      string   `json:"status"`
	OutcomeTags []string `json:"outcome_tags"`
}

type correctRequest struct {
	Title          string   `json:"title"`
	Tried          string   `json:"tried"`
	WrongBecause   string   `json:"wrong_because"`
	Fix            string   `json:"fix"`
	TriggerContext string   `json:"trigger_context"`
	Tags           []string `json:"tags"`
	Project        string   `json:"project"`
	AgentID        string   `json:"agent_id"`
	SessionID      string   `json:"session_id"`
}

type conventionRequest struct {
	Title     string   `json:"title"`
	Rule      string   `json:"rule"`
	Rationale string   `json:"rationale"`
	Example   string   `json:"example"`
	Project   string   `json:"project"`
	Tags      []string `json:"tags"`
	AgentID   string   `json:"agent_id"`
}
