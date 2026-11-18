package server

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jamestelfer/unboxed/internal/schoolbox"
)

var melbourne = mustLoad("Australia/Melbourne")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// fixtures serves the schoolbox package's testdata by path and raw query. It
// runs on the server's goroutine, so it records problems for the test
// goroutine to assert rather than failing directly.
type fixtures struct {
	routes map[string]string
	// raw answers keys that need headers a fixture file cannot carry.
	raw map[string]func() *http.Response

	mu       sync.Mutex
	requests []string
	problems []string
}

func (f *fixtures) Do(req *http.Request) (*http.Response, error) {
	key := req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, key)
	if respond, ok := f.raw[key]; ok {
		return respond(), nil
	}
	name, ok := f.routes[key]
	if !ok {
		f.problems = append(f.problems, "unexpected request "+key)
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
	}
	body, err := os.ReadFile(filepath.Join("..", "schoolbox", "testdata", name))
	if err != nil {
		f.problems = append(f.problems, fmt.Sprintf("read fixture %s: %v", name, err))
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: http.NoBody}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (f *fixtures) lastRequest() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func connect(t *testing.T, routes map[string]string) (*mcp.ClientSession, *fixtures) {
	t.Helper()
	fx := &fixtures{routes: map[string]string{"/": "home.html", "/api/user/101": "user.json"}}
	maps.Copy(fx.routes, routes)
	client, err := schoolbox.New("https://school.example", "test-token", schoolbox.WithHTTPClient(fx))
	require.NoError(t, err)
	srv, err := New(Config{
		Client:  client,
		BaseURL: "https://school.example",
		Version: "1.2.3",
		Now:     func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, melbourne) },
	})
	require.NoError(t, err)

	ctx := t.Context()
	st, ct := mcp.NewInMemoryTransports()
	_, err = srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, session.Close())
		fx.mu.Lock()
		defer fx.mu.Unlock()
		assert.Empty(t, fx.problems)
	})
	return session, fx
}

// call invokes a tool and decodes its JSON result into out. It returns the
// error text when the tool reports failure, and "" otherwise.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "content is %T", res.Content[0])
	if res.IsError {
		return text.Text
	}
	require.NoError(t, json.Unmarshal([]byte(text.Text), out), text.Text)
	return ""
}

// mustCall is call for tools expected to succeed.
func mustCall(t *testing.T, s *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	require.Empty(t, call(t, s, name, args, out))
}

var feedRoutes = map[string]string{
	"/learning/evidenceFeed/user/201":                       "feed_page1.json",
	"/learning/evidenceFeed/user/201?cursor=Y3Vyc29yLTI%3D": "feed_page2.json",
}

func TestServerReportsConfiguredVersion(t *testing.T) {
	session, _ := connect(t, nil)

	info := session.InitializeResult().ServerInfo
	require.NotNil(t, info)
	assert.Equal(t, "unboxed", info.Name)
	assert.Equal(t, "1.2.3", info.Version)
}

func TestListStudents(t *testing.T) {
	s, _ := connect(t, nil)

	var out studentsOut
	mustCall(t, s, "list_students", nil, &out)

	assert.Equal(t, []Student{{ID: 201, Name: "Alex Parent"}, {ID: 202, Name: "Sam Parent"}}, out.Students)
}

func TestStudentMustBeLinked(t *testing.T) {
	s, _ := connect(t, nil)
	for name, args := range map[string]map[string]any{
		"unknown": {"student_id": 999},
		"omitted": {},
	} {
		t.Run(name, func(t *testing.T) {
			msg := call(t, s, "list_marked_work", args, nil)
			assert.Contains(t, msg, "201 (Alex Parent), 202 (Sam Parent)")
		})
	}
}

func TestListMarkedWorkDefaultsToLastFortnight(t *testing.T) {
	s, _ := connect(t, feedRoutes)

	var out markedOut
	mustCall(t, s, "list_marked_work", map[string]any{"student_id": 201}, &out)

	assert.Equal(t, period{From: "2026-11-03", To: "2026-11-17"}, out.Period)
	require.Equal(t, 2, out.Count)
	first, second := out.Items[0], out.Items[1]
	assert.Equal(t, "Task 1", first.Title)
	assert.Equal(t, "Year 9 English", first.Subject)
	assert.Equal(t, "Ms Terry Teacher", first.Teacher)
	assert.Equal(t, new(53.0), first.Percent)
	assert.Equal(t, "C", first.Grade)
	assert.Equal(t, "Feedback 1", first.Comment)
	assert.Equal(t, "2026-11-15", first.Marked)
	assert.Equal(t, new(71.7), second.Percent)
}

func TestListMarkedWorkHonoursExplicitRange(t *testing.T) {
	s, fx := connect(t, feedRoutes)

	var out markedOut
	mustCall(t, s, "list_marked_work", map[string]any{"student_id": 201, "from": "2026-11-11", "to": "2026-11-15"}, &out)

	require.Len(t, out.Items, 1)
	assert.Equal(t, "Task 1", out.Items[0].Title)
	// Task 2 (10 Nov) precedes the range, so the second page is never fetched.
	assert.Equal(t, "/learning/evidenceFeed/user/201", fx.lastRequest())
}

func TestListMarkedWorkKeepsNewestFeedbackPerAssessment(t *testing.T) {
	s, _ := connect(t, map[string]string{"/learning/evidenceFeed/user/201": "feed_duplicate.json"})

	var out markedOut
	mustCall(t, s, "list_marked_work", map[string]any{"student_id": 201}, &out)

	require.Len(t, out.Items, 1)
	assert.Equal(t, "Feedback 1", out.Items[0].Comment)
}

func TestGradeStatsDefaultsToSemester(t *testing.T) {
	s, _ := connect(t, feedRoutes)

	var out statsOut
	mustCall(t, s, "grade_stats", map[string]any{"student_id": 201}, &out)

	assert.Equal(t, "2026-07-01", out.Period.From)
	assert.Equal(t, 3, out.Items)
	assert.Equal(t, 1, out.Unscored)
	require.NotNil(t, out.Percent)
	assert.InDelta(t, 62.4, out.Percent.Mean, 0.01)
	assert.Equal(t, map[string]int{"B": 1, "C": 1}, out.Grades)
}

func TestGetDueWorkFiltersCalendar(t *testing.T) {
	start := time.Date(2026, 11, 23, 0, 0, 0, 0, melbourne)
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, melbourne)
	s, _ := connect(t, map[string]string{
		fmt.Sprintf("/calendar/ajax/full?end=%d&start=%d&userId=201", end.Unix(), start.Unix()): "calendar.json",
	})
	args := map[string]any{"student_id": 201, "from": "2026-11-23", "to": "2026-11-29"}

	var due calendarOut
	mustCall(t, s, "get_due_work", args, &due)

	require.Len(t, due.Events, 1)
	assert.Equal(t, Event{
		Kind:         "dueWork",
		Title:        "3 - Persuasive essay",
		Subject:      "9 English A (9EngA)",
		WorkType:     "Homework",
		Start:        "2026-11-25",
		AllDay:       true,
		Type:         "Due Work",
		Detail:       "Submit via the task page.",
		Link:         "https://school.example/learning/assessments/9182/201?returnUrl=L2xlYXJuaW5n",
		AssessmentID: 9182,
	}, due.Events[0])

	var all calendarOut
	mustCall(t, s, "get_calendar", args, &all)

	require.Len(t, all.Events, 3)
	assert.Equal(t, "class", all.Events[0].Kind)
	assert.Equal(t, "schoolEvent", all.Events[2].Kind)
	assert.Equal(t, "2026-11-27", all.Events[2].Start)
}

// pastRoutes serves due work for 3–16 November and the feed back past the
// 31-day lookback before 3 November.
func pastRoutes(from time.Time) map[string]string {
	end := time.Date(2026, 11, 17, 0, 0, 0, 0, melbourne)
	routes := map[string]string{
		fmt.Sprintf("/calendar/ajax/full?end=%d&start=%d&userId=201", end.Unix(), from.Unix()): "calendar_past.json",
	}
	maps.Copy(routes, feedRoutes)
	return routes
}

func TestListAwaitingFeedbackOmitsMarkedWork(t *testing.T) {
	s, _ := connect(t, pastRoutes(time.Date(2026, 11, 3, 0, 0, 0, 0, melbourne)))

	var out awaitingOut
	mustCall(t, s, "list_awaiting_feedback", map[string]any{"student_id": 201}, &out)

	assert.Equal(t, period{From: "2026-11-03", To: "2026-11-16"}, out.Period, "last 14 days, ending yesterday")
	assert.Equal(t, 2, out.Count)
	require.Len(t, out.Items, 2)
	assert.Equal(t, Event{
		Kind:         "dueWork",
		Title:        "Lab report",
		Subject:      "Year 9 Science",
		WorkType:     "Assessment Task",
		Start:        "2026-11-12",
		AllDay:       true,
		Type:         "Due Work",
		Link:         "https://school.example/learning/assessments/1004/201",
		AssessmentID: 1004,
	}, out.Items[0])
	assert.Equal(t, 1006, out.Items[1].AssessmentID)
}

func TestListAwaitingFeedbackEndsBeforeToday(t *testing.T) {
	s, _ := connect(t, pastRoutes(time.Date(2026, 11, 3, 0, 0, 0, 0, melbourne)))

	var out awaitingOut
	mustCall(t, s, "list_awaiting_feedback", map[string]any{"student_id": 201, "from": "2026-11-03", "to": "2026-11-30"}, &out)

	assert.Equal(t, "2026-11-16", out.Period.To, "work due today or later is not yet awaiting feedback")
}

func TestListAwaitingFeedbackRejectsFutureRange(t *testing.T) {
	s, _ := connect(t, nil)

	msg := call(t, s, "list_awaiting_feedback", map[string]any{"student_id": 201, "from": "2026-11-17"}, nil)

	assert.Contains(t, msg, "before today")
}

func TestCalendarRejectsLongRanges(t *testing.T) {
	s, _ := connect(t, nil)

	msg := call(t, s, "get_calendar", map[string]any{"student_id": 201, "from": "2026-11-01", "to": "2026-12-31"}, nil)

	assert.Contains(t, msg, "longer than 31 days")
}

func TestHTMLToText(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain",
		"<p>Well done.</p><p>Next time, &amp; more</p>": "Well done.\n\nNext time, & more",
		"<ul><li>one</li><li>two</li></ul>":             "- one\n- two",
		"line<br>break":                                 "line\nbreak",
		"<p>  spaced&nbsp; out </p>":                    "spaced out",
	} {
		assert.Equal(t, want, htmlToText(in), "input %q", in)
	}
}

func withFile(t *testing.T, contentType, body string) (*mcp.ClientSession, *fixtures) {
	t.Helper()
	s, fx := connect(t, map[string]string{"/news/29655": "news_article.json"})
	fx.raw = map[string]func() *http.Response{
		"/api/session": func() *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Set-Cookie": {"PHPSESSID=s1; Path=/"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":101}`))),
			}
		},
		"/storage/fetch.php?hash=12ab34cd": func() *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {contentType}},
				Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			}
		},
	}
	return s, fx
}

var newsRoutes = map[string]string{
	"/news/lists/feed": "news_feed.json",
	"/news/topics":     "news_topics.json",
}

func TestListNewsTopicsInSchoolOrder(t *testing.T) {
	s, _ := connect(t, newsRoutes)

	var out topicsOut
	mustCall(t, s, "list_news_topics", nil, &out)

	assert.Equal(t, []NewsTopic{
		{Name: "General", Slug: "general"},
		{Name: "Sport", Slug: "sport"},
		{Name: "Camps & Excursions", Slug: "camps-excursions"},
	}, out.Topics)
}

func TestListNewsFiltersByTopicSlugOrName(t *testing.T) {
	s, _ := connect(t, newsRoutes)
	for topic, want := range map[string]int{"": 2, "sport": 29655, "General": 29700, "SPORT": 29655} {
		t.Run(topic, func(t *testing.T) {
			var out newsOut
			mustCall(t, s, "list_news", map[string]any{"topic": topic}, &out)

			if topic == "" {
				assert.Len(t, out.Articles, want)
				return
			}
			require.Len(t, out.Articles, 1)
			assert.Equal(t, want, out.Articles[0].ID)
		})
	}
}

func TestListNewsReportsArticleTopics(t *testing.T) {
	s, _ := connect(t, newsRoutes)

	var out newsOut
	mustCall(t, s, "list_news", nil, &out)

	require.Len(t, out.Articles, 2)
	assert.Equal(t, []string{"Sport"}, out.Articles[0].Topics)
}

func TestListNewsRejectsUnknownTopic(t *testing.T) {
	s, _ := connect(t, newsRoutes)

	msg := call(t, s, "list_news", map[string]any{"topic": "chess"}, nil)

	assert.Contains(t, msg, `no news topic "chess"; choose one of: general, sport, camps-excursions`)
}

const searchNews = "filter=%7B%22types%22%3A%5B%22news%22%5D%2C%22date%22%3A%7B%22from%22%3A%222026-01-01%22%7D%7D&keyword=camp"

var searchRoutes = map[string]string{
	"/api/search?" + searchNews:                       "search_page1.json",
	"/api/search?cursor=Y3Vyc29yLTI%3D&" + searchNews: "search_page2.json",
	"/api/search?keyword=camp":                        "search_page1.json",
	"/api/search?cursor=Y3Vyc29yLTI%3D&keyword=camp":  "search_page2.json",
}

func TestSearchReturnsHitsAcrossPages(t *testing.T) {
	s, _ := connect(t, searchRoutes)

	var out searchOut
	mustCall(t, s, "search", map[string]any{"query": "camp", "types": []string{"news"}, "from": "2026-01-01"}, &out)

	assert.Equal(t, 3, out.Count)
	assert.False(t, out.More)
	require.Len(t, out.Results, 3)
	assert.Equal(t, SearchHit{
		Type:      "news",
		Title:     "Year 9 Camp update",
		Date:      "2026-11-11",
		Meta:      "Posted 6 days ago by Ms Terry Teacher",
		Summary:   "...buses leave at 8:30am from the front gate...",
		Link:      "https://school.example/news/29648",
		ArticleID: 29648,
	}, out.Results[0])
	assert.Equal(t, "Year 9 / Camps", out.Results[1].Where)
	assert.Zero(t, out.Results[1].ArticleID)
	assert.Empty(t, out.Results[2].Link, "events without a url get no link")
}

func TestSearchStopsAtLimit(t *testing.T) {
	s, fx := connect(t, searchRoutes)

	var out searchOut
	mustCall(t, s, "search", map[string]any{"query": "camp", "limit": 2}, &out)

	assert.Len(t, out.Results, 2)
	assert.True(t, out.More)
	assert.Equal(t, "/api/search?keyword=camp", fx.lastRequest(), "the second page is not fetched")
}

func TestSearchValidatesArguments(t *testing.T) {
	s, fx := connect(t, nil)
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"short query":  {map[string]any{"query": " a "}, "at least 2 characters"},
		"unknown type": {map[string]any{"query": "camp", "types": []string{"homework"}}, `unknown type "homework"`},
		"bad date":     {map[string]any{"query": "camp", "from": "1 Jan"}, "from: want YYYY-MM-DD"},
		"over limit":   {map[string]any{"query": "camp", "limit": 51}, "limit must be between 1 and 50"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, call(t, s, "search", tc.args, nil), tc.want)
		})
	}
	assert.NotContains(t, fx.requests, "/api/search?keyword=camp")
}

func TestGetArticleListsAttachments(t *testing.T) {
	s, _ := connect(t, map[string]string{"/news/29655": "news_article.json"})

	var out articleOut
	mustCall(t, s, "get_article", map[string]any{"id": 29655}, &out)

	assert.Equal(t, []Attachment{{Name: "Exam timetable.pdf", Mime: "application/pdf", Size: 2048}}, out.Attachments)
}

func TestGetAttachmentReturnsContentByType(t *testing.T) {
	for name, tc := range map[string]struct {
		contentType string
		check       func(t *testing.T, c mcp.Content)
	}{
		"pdf as embedded resource": {"application/pdf", func(t *testing.T, c mcp.Content) {
			res, ok := c.(*mcp.EmbeddedResource)
			require.True(t, ok, "content is %T", c)
			assert.Equal(t, "application/pdf", res.Resource.MIMEType)
			assert.Equal(t, "https://school.example/storage/fetch.php?hash=12ab34cd", res.Resource.URI)
			assert.Equal(t, []byte("%PDF-1.7"), res.Resource.Blob)
		}},
		"image as image": {"image/png", func(t *testing.T, c mcp.Content) {
			img, ok := c.(*mcp.ImageContent)
			require.True(t, ok, "content is %T", c)
			assert.Equal(t, "image/png", img.MIMEType)
			assert.Equal(t, []byte("%PDF-1.7"), img.Data)
		}},
		"text as text": {"text/plain; charset=utf-8", func(t *testing.T, c mcp.Content) {
			txt, ok := c.(*mcp.TextContent)
			require.True(t, ok, "content is %T", c)
			assert.Equal(t, "%PDF-1.7", txt.Text)
		}},
		"octet-stream falls back to listed type": {"application/octet-stream", func(t *testing.T, c mcp.Content) {
			res, ok := c.(*mcp.EmbeddedResource)
			require.True(t, ok, "content is %T", c)
			assert.Equal(t, "application/pdf", res.Resource.MIMEType)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s, fx := withFile(t, tc.contentType, "%PDF-1.7")

			res, err := s.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "get_attachment",
				Arguments: map[string]any{"article_id": 29655, "name": "Exam timetable.pdf"},
			})

			require.NoError(t, err)
			require.False(t, res.IsError, "%+v", res.Content)
			require.Len(t, res.Content, 2)
			summary, ok := res.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Contains(t, summary.Text, "Exam timetable.pdf")
			tc.check(t, res.Content[1])
			assert.Contains(t, fx.requests, "/api/session")
		})
	}
}

func TestGetAttachmentOnlyFetchesListedFiles(t *testing.T) {
	s, fx := withFile(t, "application/pdf", "x")

	msg := call(t, s, "get_attachment", map[string]any{"article_id": 29655, "name": "secret.pdf"}, nil)

	assert.Contains(t, msg, `choose one of: "Exam timetable.pdf"`)
	assert.Equal(t, "/news/29655", fx.lastRequest(), "no download for an unlisted name")
}
