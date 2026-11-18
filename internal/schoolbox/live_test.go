//go:build live

package schoolbox

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with: (. ./.envrc.private; go test -tags live -run Live ./internal/schoolbox)
// Logs counts only, never record contents.
func TestLiveEndpointsDecode(t *testing.T) {
	token, baseURL := os.Getenv("JWT"), os.Getenv("SCHOOLBOX_URL")
	if token == "" || baseURL == "" {
		t.Skip("JWT and SCHOOLBOX_URL must be set")
	}
	c, err := New(baseURL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	me, err := c.CurrentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("students: %d", len(me.Students))

	news, err := c.News(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var downloaded bool
	for _, a := range news[:min(len(news), 10)] {
		art, err := c.Article(ctx, a.ID)
		if err != nil {
			t.Fatalf("article %d: %v", a.ID, err)
		}
		if len(art.AttachedFiles) == 0 || downloaded {
			continue
		}
		f := art.AttachedFiles[0]
		body, mime, err := c.Download(ctx, f.Links.Fetch, 20<<20)
		if err != nil {
			t.Fatalf("article %d attachment: %v", a.ID, err)
		}
		t.Logf("attachment: %d bytes (listed %d), %s", len(body), f.Size, mime)
		downloaded = true
	}
	topics, err := c.NewsTopics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("news: %d articles, %d topics", len(news), len(topics))

	for _, s := range me.Students {
		kinds := map[FeedKind]int{}
		var n int
		for item, err := range c.Feed(ctx, s.ID) {
			if err != nil {
				t.Fatal(err)
			}
			kinds[item.Kind]++
			if n++; n == 25 {
				break
			}
		}
		t.Logf("student %d: %d items, kinds %v", s.ID, n, kinds)

		now := time.Now()
		events, err := c.Calendar(ctx, s.ID, now, now.AddDate(0, 0, 14))
		if err != nil {
			t.Fatalf("student %d calendar: %v", s.ID, err)
		}
		types := map[string]int{}
		for _, e := range events {
			types[e.Meta.Variant+"/"+e.Meta.Type]++
		}
		t.Logf("student %d: %d calendar events, variant/type %v", s.ID, len(events), types)
		if n <= 10 {
			t.Errorf("student %d: want more than one page of 10", s.ID)
		}
	}
}
