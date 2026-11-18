// Package schoolbox is a read-only client for the Schoolbox API, authenticated
// with a user-scoped bearer JWT.
package schoolbox

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Doer sends HTTP requests. *http.Client satisfies it; tests substitute a fake.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	baseURL *url.URL
	token   string
	http    Doer

	// Session cookies for endpoints that ignore the bearer token; see Download.
	mu      sync.Mutex
	cookies []*http.Cookie
}

type Option func(*Client)

func WithHTTPClient(d Doer) Option {
	return func(c *Client) { c.http = d }
}

func New(baseURL, token string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("base URL %q must be absolute", baseURL)
	}
	if token == "" {
		return nil, errors.New("token is required")
	}
	c := &Client{
		baseURL: u,
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// API endpoints answer directly. A redirect means the login page,
			// which Download needs to see rather than follow.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

var (
	ErrNotFound   = errors.New("not found")
	ErrPermission = errors.New("permission denied")
)

// APIError is a non-200 response. It matches ErrNotFound or ErrPermission with errors.Is.
type APIError struct {
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.Path, e.StatusCode, cmp.Or(e.Message, http.StatusText(e.StatusCode)))
}

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrPermission:
		// Schoolbox answers 401, not 403, when a valid token lacks permission.
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	}
	return false
}

func newAPIError(path string, resp *http.Response) *APIError {
	e := &APIError{Path: path, StatusCode: resp.StatusCode}
	var problem struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(body, &problem) == nil {
		e.Message = cmp.Or(problem.Message, problem.Title)
	}
	return e
}

func (c *Client) send(ctx context.Context, path string, query url.Values, accept string) (*http.Response, error) {
	u := c.baseURL.JoinPath(path)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", accept)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, newAPIError(path, resp)
	}
	return resp, nil
}

func get[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	var v T
	resp, err := c.send(ctx, path, query, "application/json")
	if err != nil {
		return v, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.UnmarshalRead(resp.Body, &v); err != nil {
		return v, fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return v, nil
}
