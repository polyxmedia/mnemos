package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// withAuth enforces a bearer token on every route except /healthz when a
// non-empty APIKey is configured. Empty key disables auth entirely.
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

// withLogging emits a structured slog line for every request.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
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

// --- response helpers ----------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// --- typed handler helpers ----------------------------------------------

// jsonIn binds a typed request body to a handler. The handler receives a
// decoded struct and returns either a status+body+nil-error for success
// or an error. Internally maps ErrNotFound to 404 and everything else to
// the provided errStatus.
func jsonIn[In any, Out any](
	errStatus int,
	notFound error,
	fn func(r *http.Request, in In) (status int, out Out, err error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in In
		if r.Body != http.NoBody {
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("decode: %s", err))
				return
			}
		}
		status, out, err := fn(r, in)
		if err != nil {
			if notFound != nil && errors.Is(err, notFound) {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			writeErr(w, errStatus, err.Error())
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, out)
	}
}

// pathOnly wraps a handler that only uses the URL path (no body). Mirrors
// jsonIn for consistency.
func pathOnly[Out any](
	errStatus int,
	notFound error,
	fn func(r *http.Request) (status int, out Out, err error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, out, err := fn(r)
		if err != nil {
			if notFound != nil && errors.Is(err, notFound) {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			writeErr(w, errStatus, err.Error())
			return
		}
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		writeJSON(w, status, out)
	}
}
