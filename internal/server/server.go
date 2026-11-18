// Package server exposes the Schoolbox client as MCP tools for a parent
// asking about their children's timetable, due work and marks.
package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	gomime "mime"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jamestelfer/unboxed/internal/grades"
	"github.com/jamestelfer/unboxed/internal/schoolbox"
)

const maxCalendarDays = 31

type Config struct {
	Client *schoolbox.Client
	// BaseURL turns site-relative links into absolute ones.
	BaseURL string
	// Now defaults to time.Now; dates are interpreted in its location.
	Now func() time.Time
}

type service struct {
	client *schoolbox.Client
	base   *url.URL
	now    func() time.Time

	mu       sync.Mutex
	students []schoolbox.Person
}

// New builds an MCP server with the parent-facing tools registered.
func New(cfg Config) (*mcp.Server, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	s := &service{client: cfg.Client, base: base, now: cfg.Now}
	if s.now == nil {
		s.now = time.Now
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "unboxed", Title: "Schoolbox (read-only)", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: "Read-only access to Schoolbox for a parent. Call list_students first to map a child's name " +
			"(or nickname) to a student_id. Dates are YYYY-MM-DD in the school's local time; ranges are inclusive.",
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_students",
		Description: "List the children linked to the parent's account, with the student_id the other tools need.",
		Annotations: readOnly,
	}, s.listStudents)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_calendar",
		Description: "List a student's calendar between two dates: timetabled classes, school events, excursions and due work. " +
			"Defaults to the next 7 days from today. Ranges are capped at 31 days.",
		Annotations: readOnly,
	}, s.getCalendar)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_due_work",
		Description: "List homework and assessments due between two dates, taken from the student's calendar. " +
			"Defaults to the next 14 days from today. Ranges are capped at 31 days.",
		Annotations: readOnly,
	}, s.getDueWork)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_marked_work",
		Description: "List a student's marked work published between two dates, newest first, with the mark, " +
			"subject, teacher and the teacher's full written comment as plain text. Completion marks such as " +
			"\"Submitted\" or \"Not Satisfactory\" appear as status, not percent. Only the newest feedback per " +
			"assessment is kept. Defaults to the last 14 days.",
		Annotations: readOnly,
	}, s.listMarkedWork)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "grade_stats",
		Description: "Summarise a student's marks between two dates: count, mean, median, spread, quartiles, " +
			"weighted mean, grade tallies, per-subject and per-work-type breakdowns, trend and best/lowest results. " +
			"Percentages come from the normalised mark or from \"x / y\" and \"x %\" marks; completion statuses " +
			"(\"Submitted\", \"Not Satisfactory\") are tallied separately and excluded from the averages. " +
			"Descriptor grades (\"At The Expected Standard (C)\") carry a stand-in percentage; markPercent excludes them. " +
			"Defaults to the current semester (from 1 January or 1 July).",
		Annotations: readOnly,
	}, s.gradeStats)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_news",
		Description: "List school news articles addressed to the parent, optionally limited to a topic slug.",
		Annotations: readOnly,
	}, s.listNews)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_article",
		Description: "Read one news article in full, as plain text, with the names of any attached files.",
		Annotations: readOnly,
	}, s.getArticle)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_attachment",
		Description: "Download a file attached to a news article, such as a timetable or permission form. " +
			"Images return as image content, text as text, and other files (PDF, Word) as an embedded resource. " +
			"Files over 10 MB are refused.",
		Annotations: readOnly,
	}, s.getAttachment)
	return srv, nil
}

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)}

type Student struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type studentsOut struct {
	Students []Student `json:"students"`
}

func (s *service) listStudents(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, studentsOut, error) {
	people, err := s.loadStudents(ctx)
	if err != nil {
		return nil, studentsOut{}, err
	}
	out := studentsOut{Students: []Student{}}
	for _, p := range people {
		out.Students = append(out.Students, Student{ID: p.ID, Name: p.FullName})
	}
	return nil, out, nil
}

func (s *service) loadStudents(ctx context.Context) ([]schoolbox.Person, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.students != nil {
		return s.students, nil
	}
	me, err := s.client.CurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("load current user: %w", err)
	}
	s.students = me.Students
	if s.students == nil {
		s.students = []schoolbox.Person{}
	}
	return s.students, nil
}

// resolveStudent checks the id is one of the parent's children; zero picks the only child.
func (s *service) resolveStudent(ctx context.Context, id int) (schoolbox.Person, error) {
	people, err := s.loadStudents(ctx)
	if err != nil {
		return schoolbox.Person{}, err
	}
	if id == 0 && len(people) == 1 {
		return people[0], nil
	}
	for _, p := range people {
		if p.ID == id {
			return p, nil
		}
	}
	var known []string
	for _, p := range people {
		known = append(known, fmt.Sprintf("%d (%s)", p.ID, p.FullName))
	}
	return schoolbox.Person{}, fmt.Errorf("student_id %d is not a linked student; choose one of: %s", id, strings.Join(known, ", "))
}

type rangeArgs struct {
	StudentID int    `json:"student_id,omitempty" jsonschema:"student id from list_students; may be omitted when the parent has one child"`
	From      string `json:"from,omitempty" jsonschema:"first day, YYYY-MM-DD"`
	To        string `json:"to,omitempty" jsonschema:"last day (inclusive), YYYY-MM-DD"`
}

type period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// span resolves inclusive day bounds into a half-open [start, end) interval,
// filling missing bounds from the defaults.
func (s *service) span(a rangeArgs, defaultStart func(today time.Time) time.Time, defaultEnd func(start, today time.Time) time.Time) (start, end time.Time, err error) {
	loc := s.now().Location()
	today := truncateDay(s.now())
	start = defaultStart(today)
	if a.From != "" {
		if start, err = time.ParseInLocation(time.DateOnly, a.From, loc); err != nil {
			return start, end, fmt.Errorf("from: want YYYY-MM-DD: %w", err)
		}
	}
	end = defaultEnd(start, today)
	if a.To != "" {
		last, err := time.ParseInLocation(time.DateOnly, a.To, loc)
		if err != nil {
			return start, end, fmt.Errorf("to: want YYYY-MM-DD: %w", err)
		}
		end = last.AddDate(0, 0, 1)
	}
	if !end.After(start) {
		return start, end, errors.New("to must not be before from")
	}
	return start, end, nil
}

func startToday(today time.Time) time.Time { return today }

func daysAfter(n int) func(start, today time.Time) time.Time {
	return func(start, _ time.Time) time.Time { return start.AddDate(0, 0, n) }
}

func daysBefore(n int) func(today time.Time) time.Time {
	return func(today time.Time) time.Time { return today.AddDate(0, 0, -n) }
}

func throughToday(_, today time.Time) time.Time { return today.AddDate(0, 0, 1) }

func periodOf(start, end time.Time) period {
	return period{From: start.Format(time.DateOnly), To: end.AddDate(0, 0, -1).Format(time.DateOnly)}
}

func truncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

type Event struct {
	// class, dueWork, schoolEvent or other.
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Subject  string `json:"subject,omitempty"`
	WorkType string `json:"workType,omitempty"`
	Start    string `json:"start"`
	End      string `json:"end,omitempty"`
	AllDay   bool   `json:"allDay,omitempty"`
	Type     string `json:"type,omitempty"`
	Location string `json:"location,omitempty"`
	Author   string `json:"author,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Done     bool   `json:"completed,omitempty"`
	Link     string `json:"link,omitempty"`
}

type calendarOut struct {
	Student string  `json:"student"`
	Period  period  `json:"period"`
	Events  []Event `json:"events"`
}

func (s *service) getCalendar(ctx context.Context, _ *mcp.CallToolRequest, a rangeArgs) (*mcp.CallToolResult, calendarOut, error) {
	return s.calendar(ctx, a, 7, func(schoolbox.Event) bool { return true })
}

func (s *service) getDueWork(ctx context.Context, _ *mcp.CallToolRequest, a rangeArgs) (*mcp.CallToolResult, calendarOut, error) {
	return s.calendar(ctx, a, 14, schoolbox.Event.IsDueWork)
}

func (s *service) calendar(ctx context.Context, a rangeArgs, days int, keep func(schoolbox.Event) bool) (*mcp.CallToolResult, calendarOut, error) {
	student, err := s.resolveStudent(ctx, a.StudentID)
	if err != nil {
		return nil, calendarOut{}, err
	}
	start, end, err := s.span(a, startToday, daysAfter(days))
	if err != nil {
		return nil, calendarOut{}, err
	}
	if end.Sub(start) > maxCalendarDays*24*time.Hour+time.Hour {
		return nil, calendarOut{}, fmt.Errorf("range is longer than %d days; split it into smaller requests", maxCalendarDays)
	}
	events, err := s.client.Calendar(ctx, student.ID, start, end)
	if err != nil {
		return nil, calendarOut{}, fmt.Errorf("calendar: %w", err)
	}
	slices.SortStableFunc(events, func(x, y schoolbox.Event) int { return x.Start.Compare(y.Start) })
	out := calendarOut{Student: student.FullName, Period: periodOf(start, end), Events: []Event{}}
	for i := range events {
		e := &events[i]
		if !keep(*e) {
			continue
		}
		out.Events = append(out.Events, s.event(e))
	}
	return nil, out, nil
}

func (s *service) event(e *schoolbox.Event) Event {
	loc := s.now().Location()
	layout := time.RFC3339
	if e.AllDay {
		layout = time.DateOnly
	}
	out := Event{
		Kind:     eventKind(e),
		Title:    e.Title,
		Start:    e.Start.In(loc).Format(layout),
		AllDay:   e.AllDay,
		Type:     e.Meta.Type,
		Location: e.Meta.Location,
		Author:   e.Meta.Author,
		Detail:   htmlToText(e.Meta.Detail),
		Done:     e.Meta.Completed,
		Link:     s.link(e.Path),
	}
	if !e.End.IsZero() {
		out.End = e.End.In(loc).Format(layout)
	}
	if e.IsDueWork() {
		out.Title = cmp.Or(e.Meta.Title, e.Title)
		out.Subject = e.Meta.FolderName
		out.WorkType = e.Meta.WorkType
	}
	return out
}

func eventKind(e *schoolbox.Event) string {
	switch {
	case e.IsDueWork():
		return "dueWork"
	case e.Source() == schoolbox.SourceTimetable:
		return "class"
	case e.Source() == schoolbox.SourceSchool:
		return "schoolEvent"
	}
	return "other"
}

func (s *service) link(path string) string {
	if path == "" {
		return ""
	}
	ref, err := url.Parse(path)
	if err != nil {
		return path
	}
	return s.base.ResolveReference(ref).String()
}

type MarkedWork struct {
	AssessmentID int      `json:"assessmentId"`
	FeedbackID   int      `json:"feedbackId"`
	Title        string   `json:"title"`
	Subject      string   `json:"subject"`
	WorkType     string   `json:"workType,omitempty"`
	Weight       float64  `json:"weight,omitempty"`
	Due          string   `json:"due,omitempty"`
	Marked       string   `json:"marked"`
	Teacher      string   `json:"teacher"`
	Mark         string   `json:"mark"`
	Percent      *float64 `json:"percent,omitempty"`
	Grade        string   `json:"grade,omitempty"`
	Status       string   `json:"status,omitempty"`
	// Percent is a stand-in for the grade (C=70, B=85), not an earned score.
	GradeBand bool   `json:"gradeBand,omitempty"`
	Comment   string `json:"comment"`
	Link      string `json:"link,omitempty"`
}

type markedOut struct {
	Student string       `json:"student"`
	Period  period       `json:"period"`
	Count   int          `json:"count"`
	Items   []MarkedWork `json:"items"`
}

func (s *service) listMarkedWork(ctx context.Context, _ *mcp.CallToolRequest, a rangeArgs) (*mcp.CallToolResult, markedOut, error) {
	student, start, end, err := s.feedbackSpan(ctx, a, daysBefore(14))
	if err != nil {
		return nil, markedOut{}, err
	}
	items, err := s.feedback(ctx, student.ID, start, end)
	if err != nil {
		return nil, markedOut{}, err
	}
	return nil, markedOut{Student: student.FullName, Period: periodOf(start, end), Count: len(items), Items: items}, nil
}

type statsOut struct {
	Student string `json:"student"`
	Period  period `json:"period"`
	grades.Summary
}

func (s *service) gradeStats(ctx context.Context, _ *mcp.CallToolRequest, a rangeArgs) (*mcp.CallToolResult, statsOut, error) {
	student, start, end, err := s.feedbackSpan(ctx, a, semesterStart)
	if err != nil {
		return nil, statsOut{}, err
	}
	items, err := s.feedback(ctx, student.ID, start, end)
	if err != nil {
		return nil, statsOut{}, err
	}
	results := make([]grades.Result, 0, len(items))
	loc := s.now().Location()
	for i := range items {
		w := &items[i]
		marked, _ := time.ParseInLocation(time.DateOnly, w.Marked, loc)
		results = append(results, grades.Result{
			Title: w.Title, Subject: w.Subject, WorkType: w.WorkType, Weight: w.Weight, Marked: marked,
			Score: grades.Score{Percent: w.Percent, Grade: w.Grade, Status: w.Status, GradeBand: w.GradeBand},
		})
	}
	return nil, statsOut{Student: student.FullName, Period: periodOf(start, end), Summary: grades.Summarise(results)}, nil
}

// semesterStart is 1 January or 1 July, matching Australian school semesters.
func semesterStart(today time.Time) time.Time {
	m := time.January
	if today.Month() >= time.July {
		m = time.July
	}
	return time.Date(today.Year(), m, 1, 0, 0, 0, 0, today.Location())
}

// feedbackSpan resolves the student and a range ending today unless told otherwise.
func (s *service) feedbackSpan(ctx context.Context, a rangeArgs, defaultStart func(time.Time) time.Time) (schoolbox.Person, time.Time, time.Time, error) {
	student, err := s.resolveStudent(ctx, a.StudentID)
	if err != nil {
		return student, time.Time{}, time.Time{}, err
	}
	start, end, err := s.span(a, defaultStart, throughToday)
	return student, start, end, err
}

// feedback walks the newest-first evidence feed until it passes start. An
// assessment can carry several feedback records (a submission reminder, then
// the mark; a re-published comment); only the newest is kept.
func (s *service) feedback(ctx context.Context, studentID int, start, end time.Time) ([]MarkedWork, error) {
	loc := s.now().Location()
	items := []MarkedWork{}
	seen := map[int]bool{}
	for item, err := range s.client.Feed(ctx, studentID) {
		if err != nil {
			return nil, fmt.Errorf("evidence feed: %w", err)
		}
		if item.Date.Before(start) {
			break
		}
		f := item.DueWorkFeedback
		if f == nil || !item.Date.Before(end) || seen[f.Assessment.ID] {
			continue
		}
		seen[f.Assessment.ID] = true
		score := grades.Parse(f.Mark, f.NormalisedMark, f.GradientMark)
		w := MarkedWork{
			AssessmentID: f.Assessment.ID,
			FeedbackID:   f.ID,
			Title:        f.Assessment.Title,
			Subject:      f.Assessment.FolderName,
			WorkType:     f.Assessment.WorkType,
			Weight:       f.Assessment.Weight,
			Marked:       item.Date.In(loc).Format(time.DateOnly),
			Teacher:      f.Instructor.FullName,
			Mark:         f.Mark,
			Percent:      score.Percent,
			Grade:        score.Grade,
			Status:       score.Status,
			GradeBand:    score.GradeBand,
			Comment:      htmlToText(f.Comment),
			Link:         s.link(f.Links.Summary),
		}
		if !f.Assessment.DueAt.IsZero() {
			w.Due = f.Assessment.DueAt.In(loc).Format(time.DateOnly)
		}
		items = append(items, w)
	}
	return items, nil
}

type newsArgs struct {
	Topic string `json:"topic,omitempty" jsonschema:"optional topic slug"`
}

type NewsItem struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Blurb     string `json:"blurb,omitempty"`
	Author    string `json:"author"`
	Published string `json:"published"`
	Unread    bool   `json:"unread,omitempty"`
}

type newsOut struct {
	Articles []NewsItem `json:"articles"`
}

func (s *service) listNews(ctx context.Context, _ *mcp.CallToolRequest, a newsArgs) (*mcp.CallToolResult, newsOut, error) {
	list, err := s.client.News(ctx, a.Topic)
	if err != nil {
		return nil, newsOut{}, fmt.Errorf("news: %w", err)
	}
	out := newsOut{Articles: []NewsItem{}}
	loc := s.now().Location()
	for _, n := range list {
		out.Articles = append(out.Articles, NewsItem{
			ID: n.ID, Title: n.Title, Blurb: htmlToText(n.Blurb), Author: n.Author.FullName,
			Published: n.PublishAt.In(loc).Format(time.DateOnly), Unread: n.ViewedAt == nil,
		})
	}
	return nil, out, nil
}

type articleArgs struct {
	ID int `json:"id" jsonschema:"article id from list_news"`
}

type articleOut struct {
	Title       string       `json:"title"`
	Author      string       `json:"author"`
	Published   string       `json:"published"`
	Body        string       `json:"body"`
	Link        string       `json:"link"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

func (s *service) getArticle(ctx context.Context, _ *mcp.CallToolRequest, a articleArgs) (*mcp.CallToolResult, articleOut, error) {
	art, err := s.client.Article(ctx, a.ID)
	if err != nil {
		return nil, articleOut{}, fmt.Errorf("article %d: %w", a.ID, err)
	}
	out := articleOut{
		Title:     art.Title,
		Author:    art.Author.FullName,
		Published: art.PublishAt.In(s.now().Location()).Format(time.DateOnly),
		Body:      htmlToText(art.Body),
		Link:      s.link(fmt.Sprintf("/news/%d", art.ID)),
	}
	for i := range art.AttachedFiles {
		f := &art.AttachedFiles[i]
		out.Attachments = append(out.Attachments, Attachment{Name: f.Name, Mime: f.Mime, Size: f.Size})
	}
	return nil, out, nil
}

const maxAttachmentBytes = 10 << 20

type attachmentArgs struct {
	ArticleID int    `json:"article_id" jsonschema:"article id from list_news"`
	Name      string `json:"name" jsonschema:"attachment name exactly as get_article lists it"`
}

// getAttachment only fetches files the article lists, so the tool cannot be
// pointed at arbitrary storage links.
func (s *service) getAttachment(ctx context.Context, _ *mcp.CallToolRequest, a attachmentArgs) (*mcp.CallToolResult, any, error) {
	art, err := s.client.Article(ctx, a.ArticleID)
	if err != nil {
		return nil, nil, fmt.Errorf("article %d: %w", a.ArticleID, err)
	}
	var file *schoolbox.File
	names := make([]string, 0, len(art.AttachedFiles))
	for i := range art.AttachedFiles {
		names = append(names, fmt.Sprintf("%q", art.AttachedFiles[i].Name))
		if art.AttachedFiles[i].Name == a.Name {
			file = &art.AttachedFiles[i]
		}
	}
	if file == nil {
		if len(names) == 0 {
			return nil, nil, fmt.Errorf("article %d has no attachments", a.ArticleID)
		}
		return nil, nil, fmt.Errorf("article %d has no attachment %q; choose one of: %s", a.ArticleID, a.Name, strings.Join(names, ", "))
	}
	if file.Size > maxAttachmentBytes {
		return nil, nil, fmt.Errorf("%q is %d bytes, over the %d byte limit", file.Name, file.Size, maxAttachmentBytes)
	}
	body, contentType, err := s.client.Download(ctx, file.Links.Fetch, maxAttachmentBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("download %q: %w", file.Name, err)
	}
	mime := file.Mime
	if mt, _, err := gomime.ParseMediaType(contentType); err == nil && mt != "application/octet-stream" {
		mime = mt
	}
	summary := &mcp.TextContent{Text: fmt.Sprintf("%s (%s, %d bytes) from article %d", file.Name, mime, len(body), a.ArticleID)}
	var content mcp.Content
	switch {
	case strings.HasPrefix(mime, "image/"):
		content = &mcp.ImageContent{Data: body, MIMEType: mime}
	case strings.HasPrefix(mime, "text/") && utf8.Valid(body):
		content = &mcp.TextContent{Text: string(body)}
	default:
		content = &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI: s.link(file.Links.Fetch), MIMEType: mime, Blob: body,
		}}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{summary, content}}, nil, nil
}
