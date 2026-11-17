package schoolbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type route struct {
	status  int
	fixture string
}

// fakeDoer serves fixtures keyed by request path plus raw query, and records requests.
type fakeDoer struct {
	t        *testing.T
	routes   map[string]route
	requests []*http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)
	key := req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	r, ok := f.routes[key]
	if !ok {
		f.t.Errorf("unexpected request %s", key)
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
	}
	body, err := os.ReadFile(filepath.Join("testdata", r.fixture))
	if err != nil {
		f.t.Fatalf("read fixture: %v", err)
	}
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func newTestClient(t *testing.T, routes map[string]route) (*Client, *fakeDoer) {
	t.Helper()
	doer := &fakeDoer{t: t, routes: routes}
	c, err := New("https://school.example", "test-token", WithHTTPClient(doer))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, doer
}

func ok(fixture string) route { return route{http.StatusOK, fixture} }

func TestNewRejectsInvalidConfig(t *testing.T) {
	for name, tc := range map[string]struct{ base, token string }{
		"relative base": {"school.example", "t"},
		"empty token":   {"https://school.example", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(tc.base, tc.token); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRequestsCarryBearerTokenAndAccept(t *testing.T) {
	c, doer := newTestClient(t, map[string]route{"/news/topics": ok("news_topics.json")})

	if _, err := c.NewsTopics(context.Background()); err != nil {
		t.Fatal(err)
	}

	req := doer.requests[0]
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
}

func TestCurrentUserResolvesInternalIDFromHomepage(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{
		"/":             ok("home.html"),
		"/api/user/101": ok("user.json"),
	})

	u, err := c.CurrentUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if u.ID != 101 || u.ExternalID != "9001" || u.Role.Type != "parent" {
		t.Errorf("user = %+v", u)
	}
	if len(u.Students) != 2 || u.Students[0].ID != 201 || u.Students[1].FullName != "Sam Parent" {
		t.Errorf("students = %+v", u.Students)
	}
}

func TestPermissionDeniedMatchesErrPermission(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{
		"/api/user/201": {http.StatusUnauthorized, "permission_denied.json"},
	})

	_, err := c.User(context.Background(), 201)

	if !errors.Is(err, ErrPermission) {
		t.Fatalf("err = %v, want ErrPermission", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "You do not have permission to view this page." {
		t.Errorf("APIError = %+v", apiErr)
	}
}

func feedRoutes() map[string]route {
	return map[string]route{
		"/learning/evidenceFeed/user/201":                       ok("feed_page1.json"),
		"/learning/evidenceFeed/user/201?cursor=Y3Vyc29yLTI%3D": ok("feed_page2.json"),
	}
}

func TestFeedFollowsCursorAcrossPages(t *testing.T) {
	c, doer := newTestClient(t, feedRoutes())

	var items []FeedItem
	for item, err := range c.Feed(context.Background(), 201) {
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}

	if len(items) != 4 || len(doer.requests) != 2 {
		t.Fatalf("got %d items from %d requests, want 4 from 2", len(items), len(doer.requests))
	}
	first := items[0].DueWorkFeedback
	if first == nil || first.Mark != "53 %" || *first.NormalisedMark != 53 || first.Assessment.FolderName != "Year 9 English" {
		t.Errorf("first item = %+v", first)
	}
	if want := time.Date(2026, 10, 15, 4, 30, 0, 0, time.UTC); !first.Assessment.DueAt.Equal(want) {
		t.Errorf("DueAt = %v, want %v", first.Assessment.DueAt, want)
	}
	if items[1].DueWorkFeedback.NormalisedMark != nil {
		t.Error("null normalisedMark should decode to nil")
	}
}

func TestFeedKeepsUnmodelledKindsRaw(t *testing.T) {
	c, _ := newTestClient(t, feedRoutes())

	var last FeedItem
	for item, err := range c.Feed(context.Background(), 201) {
		if err != nil {
			t.Fatal(err)
		}
		last = item
	}

	if last.Kind != KindBadgeAward || last.DueWorkFeedback != nil || !bytes.Contains(last.Object, []byte(`"Star"`)) {
		t.Errorf("last = %+v", last)
	}
}

func TestFeedStopsFetchingWhenCallerBreaks(t *testing.T) {
	c, doer := newTestClient(t, feedRoutes())

	for range c.Feed(context.Background(), 201) {
		break
	}

	if len(doer.requests) != 1 {
		t.Errorf("made %d requests, want 1", len(doer.requests))
	}
}

func TestFeedYieldsPageErrorAndStops(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{
		"/learning/evidenceFeed/user/201":                       ok("feed_page1.json"),
		"/learning/evidenceFeed/user/201?cursor=Y3Vyc29yLTI%3D": {http.StatusUnauthorized, "permission_denied.json"},
	})

	var n int
	var gotErr error
	for _, err := range c.Feed(context.Background(), 201) {
		if err != nil {
			gotErr = err
			continue
		}
		n++
	}

	if n != 2 || !errors.Is(gotErr, ErrPermission) {
		t.Errorf("got %d items and err %v, want 2 and ErrPermission", n, gotErr)
	}
}

func TestNewsAndArticle(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{
		"/news/lists/feed?topic=sport": ok("news_feed.json"),
		"/news/29655":                  ok("news_article.json"),
	})
	ctx := context.Background()

	list, err := c.News(ctx, "sport")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Author.FullName != "Ms Terry Teacher" || list[0].ViewedAt != nil || list[0].Attachments != 1 {
		t.Errorf("list = %+v", list)
	}

	a, err := c.Article(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Body != "<p>Bring a hat.</p>" || a.Author.FullName != "Ms Terry Teacher" {
		t.Errorf("article = %+v", a)
	}
}
