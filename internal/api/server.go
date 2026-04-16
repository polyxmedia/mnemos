// Package api exposes the Mnemos services over HTTP for multi-agent and
// remote setups. The transport is a thin adapter — every handler
// delegates to a service method. No business logic here.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Server owns the HTTP handler tree.
type Server struct {
	mem     *memory.Service
	sess    *session.Service
	skill   *skills.Service
	touches memory.TouchStore
	prewarm *prewarm.Service
	log     *slog.Logger
	apiKey  string
}

// Config bundles dependencies. APIKey is optional; when empty, auth is off.
type Config struct {
	Memory   *memory.Service
	Sessions *session.Service
	Skills   *skills.Service
	Touches  memory.TouchStore
	Prewarm  *prewarm.Service
	Logger   *slog.Logger
	APIKey   string
}

// NewServer constructs the HTTP server. Call Handler() for use with
// net/http.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		mem:     cfg.Memory,
		sess:    cfg.Sessions,
		skill:   cfg.Skills,
		touches: cfg.Touches,
		prewarm: cfg.Prewarm,
		log:     cfg.Logger,
		apiKey:  cfg.APIKey,
	}
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("POST /v1/observations", s.saveObservation)
	mux.HandleFunc("GET /v1/observations/{id}", s.getObservation)
	mux.HandleFunc("DELETE /v1/observations/{id}", s.deleteObservation)

	mux.HandleFunc("POST /v1/search", s.search)
	mux.HandleFunc("POST /v1/context", s.context)
	mux.HandleFunc("POST /v1/link", s.link)

	mux.HandleFunc("POST /v1/sessions", s.sessionStart)
	mux.HandleFunc("POST /v1/sessions/{id}/close", s.sessionEnd)

	mux.HandleFunc("POST /v1/skills", s.skillSave)
	mux.HandleFunc("POST /v1/skills/match", s.skillMatch)

	mux.HandleFunc("POST /v1/correct", s.correct)
	mux.HandleFunc("POST /v1/convention", s.convention)
	mux.HandleFunc("POST /v1/touch", s.touch)

	mux.HandleFunc("GET /v1/stats", s.stats)

	return s.withAuth(s.withLogging(mux))
}

// --- middleware ---------------------------------------------------------

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != s.apiKey {
			writeErr(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(sr, r)
		s.log.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", sr.status, "dur", time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// --- handlers -----------------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) saveObservation(w http.ResponseWriter, r *http.Request) {
	var in memory.SaveInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.mem.Save(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, res)
}

func (s *Server) getObservation(w http.ResponseWriter, r *http.Request) {
	o, err := s.mem.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, o)
}

func (s *Server) deleteObservation(w http.ResponseWriter, r *http.Request) {
	if err := s.mem.Delete(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var in memory.SearchInput
	if !decode(w, r, &in) {
		return
	}
	results, err := s.mem.Search(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

func (s *Server) context(w http.ResponseWriter, r *http.Request) {
	var in memory.ContextInput
	if !decode(w, r, &in) {
		return
	}
	block, err := s.mem.Context(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, block)
}

func (s *Server) link(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
		LinkType string `json:"link_type"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.mem.Link(r.Context(), in.SourceID, in.TargetID, memory.LinkType(in.LinkType)); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) sessionStart(w http.ResponseWriter, r *http.Request) {
	var in session.OpenInput
	if !decode(w, r, &in) {
		return
	}
	sess, err := s.sess.Open(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
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
	writeJSON(w, 201, out)
}

func (s *Server) sessionEnd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Summary     string   `json:"summary"`
		Reflection  string   `json:"reflection"`
		Status      string   `json:"status"`
		OutcomeTags []string `json:"outcome_tags"`
	}
	if !decode(w, r, &body) {
		return
	}
	status := session.Status(body.Status)
	if status == "" {
		status = session.StatusOK
	}
	if err := s.sess.Close(r.Context(), session.CloseInput{
		ID: r.PathValue("id"), Summary: body.Summary, Reflection: body.Reflection,
		Status: status, OutcomeTags: body.OutcomeTags,
	}); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) skillSave(w http.ResponseWriter, r *http.Request) {
	var in skills.SaveInput
	if !decode(w, r, &in) {
		return
	}
	sk, err := s.skill.Save(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, sk)
}

func (s *Server) skillMatch(w http.ResponseWriter, r *http.Request) {
	var in skills.MatchInput
	if !decode(w, r, &in) {
		return
	}
	matches, err := s.skill.Match(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"matches": matches})
}

func (s *Server) correct(w http.ResponseWriter, r *http.Request) {
	var body struct {
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
	if !decode(w, r, &body) {
		return
	}
	content := fmt.Sprintf("**Tried:** %s\n\n**Wrong because:** %s\n\n**Fix:** %s",
		body.Tried, body.WrongBecause, body.Fix)
	res, err := s.mem.Save(r.Context(), memory.SaveInput{
		Title: body.Title, Content: content, Type: memory.TypeCorrection,
		Tags: body.Tags, Project: body.Project, AgentID: body.AgentID,
		SessionID: body.SessionID, Importance: 8,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, res)
}

func (s *Server) convention(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string   `json:"title"`
		Rule      string   `json:"rule"`
		Rationale string   `json:"rationale"`
		Example   string   `json:"example"`
		Project   string   `json:"project"`
		Tags      []string `json:"tags"`
		AgentID   string   `json:"agent_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	content := body.Rule
	if body.Example != "" {
		content = content + "\n\nExample:\n" + body.Example
	}
	res, err := s.mem.Save(r.Context(), memory.SaveInput{
		Title: body.Title, Content: content, Type: memory.TypeConvention,
		Tags: body.Tags, Project: body.Project, AgentID: body.AgentID,
		Importance: 8, Rationale: body.Rationale,
	})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, res)
}

func (s *Server) touch(w http.ResponseWriter, r *http.Request) {
	if s.touches == nil {
		writeErr(w, 400, "touch store not wired")
		return
	}
	var in memory.TouchInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.touches.Record(r.Context(), in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.mem.Stats(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// --- helpers ------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, fmt.Sprintf("decode: %s", err))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Serve runs the HTTP server until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http listen", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
