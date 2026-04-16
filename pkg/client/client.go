// Package client is the typed Go client for the Mnemos HTTP API.
//
// Use this in Go programs that want to read/write Mnemos over the network
// (multi-agent setups, remote deployments, CI glue). For local in-process
// use, import the service packages directly — they are the same types this
// client mirrors on the wire.
//
// Example:
//
//	c := client.New("http://localhost:8080", client.WithAPIKey(key))
//	res, err := c.Save(ctx, client.SaveInput{
//		Title:   "use WAL",
//		Content: "enables concurrent readers",
//		Type:    "pattern",
//	})
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a typed HTTP client for the Mnemos API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithAPIKey sets the bearer token sent as Authorization: Bearer <key>.
func WithAPIKey(key string) Option { return func(c *Client) { c.apiKey = key } }

// WithHTTPClient lets callers supply a pre-configured http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithTimeout sets the request timeout on the default client.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.httpClient = &http.Client{Timeout: d} }
}

// New constructs a Client. baseURL is the API origin (e.g. "http://localhost:8080").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// --- request types (mirror the API wire format) ------------------------

// SaveInput is the payload for POST /v1/observations.
type SaveInput struct {
	Title      string   `json:"Title"`
	Content    string   `json:"Content"`
	Type       string   `json:"Type"`
	Tags       []string `json:"Tags,omitempty"`
	Importance int      `json:"Importance,omitempty"`
	TTLDays    int      `json:"TTLDays,omitempty"`
	Project    string   `json:"Project,omitempty"`
	AgentID    string   `json:"AgentID,omitempty"`
	SessionID  string   `json:"SessionID,omitempty"`
	Rationale  string   `json:"Rationale,omitempty"`
	Structured string   `json:"Structured,omitempty"`
}

// SaveResult is the response from POST /v1/observations.
type SaveResult struct {
	Observation Observation `json:"Observation"`
	Deduped     bool        `json:"Deduped"`
}

// Observation is the client-side view of a stored observation.
type Observation struct {
	ID          string    `json:"ID"`
	Title       string    `json:"Title"`
	Content     string    `json:"Content"`
	Type        string    `json:"Type"`
	Tags        []string  `json:"Tags"`
	Importance  int       `json:"Importance"`
	AccessCount int       `json:"AccessCount"`
	Project     string    `json:"Project"`
	SessionID   string    `json:"SessionID"`
	CreatedAt   time.Time `json:"CreatedAt"`
}

// SearchInput is the payload for POST /v1/search.
type SearchInput struct {
	Query         string   `json:"Query"`
	Type          string   `json:"Type,omitempty"`
	Tags          []string `json:"Tags,omitempty"`
	MinImportance int      `json:"MinImportance,omitempty"`
	Limit         int      `json:"Limit,omitempty"`
	AgentID       string   `json:"AgentID,omitempty"`
	Project       string   `json:"Project,omitempty"`
	IncludeStale  bool     `json:"IncludeStale,omitempty"`
}

// SearchHit is one ranked search result.
type SearchHit struct {
	Observation Observation `json:"Observation"`
	Score       float64     `json:"Score"`
	BM25        float64     `json:"BM25"`
	Snippet     string      `json:"Snippet"`
}

// SessionStartInput is the payload for POST /v1/sessions.
type SessionStartInput struct {
	Project string `json:"Project,omitempty"`
	Goal    string `json:"Goal,omitempty"`
	AgentID string `json:"AgentID,omitempty"`
}

// SessionStartResult includes the pre-warm block when available.
type SessionStartResult struct {
	SessionID string        `json:"session_id"`
	StartedAt time.Time     `json:"started_at"`
	Prewarm   *PrewarmBlock `json:"prewarm,omitempty"`
}

// PrewarmBlock is the pushed context returned by session_start.
type PrewarmBlock struct {
	Text          string `json:"Text"`
	TokenEstimate int    `json:"TokenEstimate"`
}

// --- methods -----------------------------------------------------------

// Save creates (or dedups) an observation.
func (c *Client) Save(ctx context.Context, in SaveInput) (*SaveResult, error) {
	var out SaveResult
	if err := c.do(ctx, "POST", "/v1/observations", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns an observation by ID.
func (c *Client) Get(ctx context.Context, id string) (*Observation, error) {
	var out Observation
	if err := c.do(ctx, "GET", "/v1/observations/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes an observation by ID.
func (c *Client) Delete(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/observations/"+id, nil, nil)
}

// Search runs a ranked memory search.
func (c *Client) Search(ctx context.Context, in SearchInput) ([]SearchHit, error) {
	var out struct {
		Results []SearchHit `json:"results"`
	}
	if err := c.do(ctx, "POST", "/v1/search", in, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// SessionStart opens a session and returns the pre-warm block.
func (c *Client) SessionStart(ctx context.Context, in SessionStartInput) (*SessionStartResult, error) {
	var out SessionStartResult
	if err := c.do(ctx, "POST", "/v1/sessions", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionEnd closes a session.
func (c *Client) SessionEnd(ctx context.Context, id, summary, reflection, status string) error {
	body := map[string]any{
		"summary":    summary,
		"reflection": reflection,
		"status":     status,
	}
	return c.do(ctx, "POST", "/v1/sessions/"+id+"/close", body, nil)
}

// Healthz probes /healthz. Returns nil on 200.
func (c *Client) Healthz(ctx context.Context) error {
	return c.do(ctx, "GET", "/healthz", nil, nil)
}

// --- transport ---------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("client: call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(resp.Body)
		return &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("client: decode: %w", err)
	}
	return nil
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Status int
	Body   string
}

// Error implements the error interface.
func (e *APIError) Error() string {
	return fmt.Sprintf("mnemos api: status %d: %s", e.Status, e.Body)
}

// IsNotFound reports whether err is an APIError with 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusNotFound
	}
	return false
}
