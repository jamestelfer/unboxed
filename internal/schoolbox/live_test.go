//go:build live

package schoolbox

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with: go test -tags live -run Live ./internal/schoolbox
// SCHOOLBOX_TOKEN and SCHOOLBOX_URL come from the environment, which direnv provides.
// Logs counts only, never record contents.
func TestLiveEndpointsDecode(t *testing.T) {
	token, baseURL := os.Getenv("SCHOOLBOX_TOKEN"), os.Getenv("SCHOOLBOX_URL")
	if token == "" || baseURL == "" {
		t.Skip("SCHOOLBOX_TOKEN and SCHOOLBOX_URL must be set")
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

	news, err := c.News(ctx)
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
		var marked []int
		for item, err := range c.Feed(ctx, s.ID) {
			if err != nil {
				t.Fatal(err)
			}
			kinds[item.Kind]++
			if f := item.DueWorkFeedback; f != nil {
				marked = append(marked, f.Assessment.ID)
			}
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
		liveDueAssessment(t, c, s.ID, events)
		liveSubmissionDownload(t, c, s.ID, marked)
		if n <= 10 {
			t.Errorf("student %d: want more than one page of 10", s.ID)
		}
	}
}

// liveDueAssessment scrapes the first due-work assessment in events.
func liveDueAssessment(t *testing.T, c *Client, studentID int, events []Event) {
	t.Helper()
	for _, e := range events {
		if !e.IsDueWork() || e.AssessmentID == 0 {
			continue
		}
		logAssessment(t, c, e.AssessmentID, studentID)
		return
	}
	t.Logf("student %d: no due work with an assessment link", studentID)
}

// liveSubmissionDownload scrapes marked assessments until one lists a
// submission file, then downloads it to confirm the session cookie covers
// submission links as well as the file store.
func liveSubmissionDownload(t *testing.T, c *Client, studentID int, assessmentIDs []int) {
	t.Helper()
	for _, id := range assessmentIDs {
		page := logAssessment(t, c, id, studentID)
		for _, h := range page.History {
			if len(h.Files) == 0 {
				continue
			}
			body, mime, err := c.Download(context.Background(), h.Files[0].Link, 20<<20)
			if err != nil {
				t.Fatalf("student %d submission download: %v", studentID, err)
			}
			t.Logf("student %d submission: %d bytes, %s", studentID, len(body), mime)
			return
		}
	}
	t.Logf("student %d: no marked assessment with a submission file", studentID)
}

func logAssessment(t *testing.T, c *Client, assessmentID, studentID int) AssessmentPage {
	t.Helper()
	page, err := c.Assessment(context.Background(), assessmentID, studentID, time.Local)
	if err != nil {
		t.Fatalf("student %d assessment: %v", studentID, err)
	}
	if page.Title == "" {
		t.Errorf("student %d assessment: empty title", studentID)
	}
	t.Logf("student %d assessment: %d task files, %d grading, %d history, %d rubrics, warnings %v",
		studentID, len(page.TaskFiles), len(page.Grading), len(page.History), len(page.Rubrics), page.Warnings)
	return page
}
