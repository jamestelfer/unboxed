//go:build live

package schoolbox

import (
	"context"
	"os"
	"testing"
)

// Run with: set -a; source .env.local; set +a; go test -tags live -run Live ./internal/schoolbox
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
	for _, a := range news[:min(len(news), 5)] {
		if _, err := c.Article(ctx, a.ID); err != nil {
			t.Fatalf("article %d: %v", a.ID, err)
		}
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
		if n <= 10 {
			t.Errorf("student %d: want more than one page of 10", s.ID)
		}
	}
}
