// Package client talks to the web API (v2) of a TP-Link Omada controller,
// including the controller built into Omada Fusion gateways.
//
// The integrated Fusion controller exposes the same /{omadacId}/api/v2 API
// that its web UI uses. Authentication is a local controller account:
// POST /{omadacId}/api/v2/login returns a CSRF token, and the session
// cookie plus a Csrf-Token header authorize later calls.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config holds connection settings for a controller.
type Config struct {
	// URL is the controller base URL, for example https://192.168.0.1.
	URL      string
	Username string
	Password string
	// Insecure skips TLS verification. Integrated controllers ship a
	// self-signed certificate, so this is commonly needed.
	Insecure bool
	Timeout  time.Duration
	// UserAgent is sent with every request.
	UserAgent string
}

// Client is a logged-in session against one controller. It is safe for
// concurrent use; Terraform calls resources in parallel.
type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client

	mu        sync.Mutex
	controlID string
	token     string
	info      *ControllerInfo
}

// APIError is a non-zero errorCode returned inside the JSON envelope.
type APIError struct {
	Method  string
	Path    string
	Code    int
	Message string
	Status  int
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("omada %s %s: errorCode %d: %s", e.Method, e.Path, e.Code, e.Message)
	}
	return fmt.Sprintf("omada %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// IsNotFound reports whether err means the object does not exist.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// NotFoundError is returned when a lookup finds no matching object.
type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What + " not found" }

// Session-expired error codes observed on 6.x controllers.
var reloginCodes = map[int]bool{-1200: true, -1201: true, -1202: true, -44112: true}

type envelope struct {
	ErrorCode int             `json:"errorCode"`
	Msg       string          `json:"msg"`
	Result    json.RawMessage `json:"result"`
}

// New builds a client. It does not contact the controller until the first call.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("controller URL is required")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("controller username and password are required")
	}
	u, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid controller URL %q", cfg.URL)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // opt-in for self-signed controllers
	return &Client{
		cfg:  cfg,
		base: u,
		http: &http.Client{Jar: jar, Transport: tr, Timeout: cfg.Timeout},
	}, nil
}

// ControllerInfo is the unauthenticated /api/info response.
type ControllerInfo struct {
	ControllerVersion string `json:"controllerVer"`
	APIVersion        string `json:"apiVer"`
	Configured        bool   `json:"configured"`
	Type              int    `json:"type"`
	OmadacID          string `json:"omadacId"`
	Category          string `json:"omadacCategory"`
}

// Info returns controller identity. The result is cached.
func (c *Client) Info(ctx context.Context) (*ControllerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.infoLocked(ctx)
}

func (c *Client) infoLocked(ctx context.Context) (*ControllerInfo, error) {
	if c.info != nil {
		return c.info, nil
	}
	var info ControllerInfo
	if err := c.raw(ctx, http.MethodGet, "/api/info", nil, &info, false); err != nil {
		return nil, fmt.Errorf("reading controller info: %w", err)
	}
	if info.OmadacID == "" {
		return nil, errors.New("controller /api/info did not return an omadacId; is this an Omada controller?")
	}
	c.info = &info
	c.controlID = info.OmadacID
	return c.info, nil
}

// loginLocked logs in, retrying transport errors (an admin client on Wi-Fi
// can lose connectivity for a few seconds while APs re-provision).
func (c *Client) loginLocked(ctx context.Context) error {
	err := c.loginOnceLocked(ctx)
	for attempt := 1; err != nil && isTransportErr(err) && attempt <= 4; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
		err = c.loginOnceLocked(ctx)
	}
	return err
}

func (c *Client) loginOnceLocked(ctx context.Context) error {
	if _, err := c.infoLocked(ctx); err != nil {
		return err
	}
	body := map[string]string{"username": c.cfg.Username, "password": c.cfg.Password}
	var res struct {
		Token string `json:"token"`
	}
	c.token = ""
	if err := c.raw(ctx, http.MethodPost, "/"+c.controlID+"/api/v2/login", body, &res, false); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	if res.Token == "" {
		return errors.New("login succeeded but no CSRF token was returned")
	}
	c.token = res.Token
	return nil
}

// Do calls an API path relative to /{omadacId}/api/v2, for example
// "/sites". body is JSON-encoded when non-nil, and the envelope's result is
// decoded into out when out is non-nil. A single re-login is attempted when
// the session has expired.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	c.mu.Lock()
	if c.token == "" {
		if err := c.loginLocked(ctx); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	full := "/" + c.controlID + "/api/v2" + path
	c.mu.Unlock()

	err := c.raw(ctx, method, full, body, out, true)
	// Creating or editing SSIDs re-provisions APs, which can briefly drop a
	// wireless admin client. Retry reads on transport errors only; writes are
	// not retried because they may already have been applied.
	for attempt := 1; err != nil && method == http.MethodGet && isTransportErr(err) && attempt <= 4; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
		err = c.raw(ctx, method, full, body, out, true)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (reloginCodes[apiErr.Code] || apiErr.Status == http.StatusUnauthorized) {
		c.mu.Lock()
		lerr := c.loginLocked(ctx)
		c.mu.Unlock()
		if lerr != nil {
			return lerr
		}
		return c.raw(ctx, method, full, body, out, true)
	}
	return err
}

func (c *Client) raw(ctx context.Context, method, path string, body, out any, auth bool) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := *c.base
	if i := strings.IndexByte(path, '?'); i >= 0 {
		u.Path = c.base.Path + path[:i]
		u.RawQuery = path[i+1:]
	} else {
		u.Path = c.base.Path + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if auth && c.token != "" {
		req.Header.Set("Csrf-Token", c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	pathNoQuery := strings.SplitN(path, "?", 2)[0]
	if resp.StatusCode >= 400 {
		return &APIError{Method: method, Path: pathNoQuery, Status: resp.StatusCode, Message: snippet(data)}
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return &APIError{Method: method, Path: pathNoQuery, Status: resp.StatusCode, Message: "response was not JSON: " + snippet(data)}
	}
	if env.ErrorCode != 0 {
		return &APIError{Method: method, Path: pathNoQuery, Code: env.ErrorCode, Message: env.Msg, Status: resp.StatusCode}
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decoding %s %s: %w", method, pathNoQuery, err)
		}
	}
	return nil
}

// isTransportErr reports errors below the HTTP layer (connection refused,
// reset, timeout), as opposed to controller replies.
func isTransportErr(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return false
	}
	var nerr net.Error
	var uerr *url.Error
	return errors.As(err, &nerr) || errors.As(err, &uerr)
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// page is the paged list shape used by most v2 list endpoints.
type page[T any] struct {
	TotalRows   int `json:"totalRows"`
	CurrentPage int `json:"currentPage"`
	Data        []T `json:"data"`
}

// listAll walks a paged endpoint. path must not already have paging params.
func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	for p := 1; p <= 1000; p++ {
		var pg page[T]
		if err := c.Do(ctx, http.MethodGet, fmt.Sprintf("%s%scurrentPage=%d&currentPageSize=100", path, sep, p), nil, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg.Data...)
		// Some endpoints omit totalRows; stop on a short page instead.
		if len(pg.Data) < 100 || (pg.TotalRows > 0 && len(all) >= pg.TotalRows) {
			break
		}
	}
	return all, nil
}
