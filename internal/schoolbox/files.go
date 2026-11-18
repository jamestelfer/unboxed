package schoolbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// File is an upload in Schoolbox's file store, as attached to an article.
type File struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	Mime string `json:"mime"`
	// One of image, video, audio, document or other.
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Links struct {
		// Site-relative download link.
		Fetch string `json:"fetch"`
	} `json:"_links"`
}

// ErrTooLarge is returned by Download when a file exceeds the caller's limit.
var ErrTooLarge = errors.New("file too large")

// Download fetches a site-relative file link such as File.Links.Fetch, up to
// limit bytes. The file store ignores bearer tokens and redirects to the login
// page, so Download exchanges the token for a session cookie on first use and
// again if the session lapses. It returns the body and its content type.
func (c *Client) Download(ctx context.Context, link string, limit int64) ([]byte, string, error) {
	u, err := c.siteURL(link)
	if err != nil {
		return nil, "", err
	}
	for attempt := 0; ; attempt++ {
		cookies, err := c.sessionCookies(ctx, attempt > 0)
		if err != nil {
			return nil, "", err
		}
		body, mime, err := c.download(ctx, u, cookies, limit)
		if errors.Is(err, errLoginRequired) && attempt == 0 {
			continue
		}
		return body, mime, err
	}
}

var errLoginRequired = errors.New("redirected to login")

func (c *Client) download(ctx context.Context, u *url.URL, cookies []*http.Cookie, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("GET %s: %w", u.Path, err)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("GET %s: %w", u.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400,
		resp.StatusCode == http.StatusUnauthorized:
		return nil, "", fmt.Errorf("GET %s: %w", u.Path, errLoginRequired)
	case resp.StatusCode != http.StatusOK:
		return nil, "", newAPIError(u.Path, resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("GET %s: %w", u.Path, err)
	}
	if int64(len(body)) > limit {
		return nil, "", fmt.Errorf("GET %s: %w (limit %d bytes)", u.Path, ErrTooLarge, limit)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// siteURL resolves a site-relative link, refusing links to other hosts so a
// crafted link cannot send the session elsewhere.
func (c *Client) siteURL(link string) (*url.URL, error) {
	ref, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("parse link %q: %w", link, err)
	}
	if ref.IsAbs() || ref.Host != "" || !strings.HasPrefix(ref.Path, "/") {
		return nil, fmt.Errorf("link %q must be site-relative", link)
	}
	return c.baseURL.ResolveReference(ref), nil
}

// sessionCookies returns cached session cookies, logging in when there are
// none or when refresh is set.
func (c *Client) sessionCookies(ctx context.Context, refresh bool) ([]*http.Cookie, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cookies != nil && !refresh {
		return c.cookies, nil
	}
	const path = "/api/session"
	form := url.Values{"jwt": {c.token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL.JoinPath(path).String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, newAPIError(path, resp)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("POST %s: no session cookie in response", path)
	}
	c.cookies = cookies
	return cookies, nil
}
