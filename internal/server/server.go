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
	"strconv"
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
	// Version is reported to MCP clients as the server version.
	Version string
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
	srv := mcp.NewServer(&mcp.Implementation{Name: "unboxed", Title: "Schoolbox (read-only)", Version: cfg.Version}, &mcp.ServerOptions{
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
			"Each item carries the assessmentId that get_assessment takes, and completed shows whether the student has ticked it off. " +
			"Defaults to the next 14 days from today. Ranges are capped at 31 days.",
		Annotations: readOnly,
	}, s.getDueWork)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_awaiting_feedback",
		Description: "List due work whose due date has passed but which has no mark or feedback in the evidence feed yet. " +
			"It does not show whether the work was submitted: Schoolbox does not report submission status here. " +
			"Defaults to the last 14 days, ending yesterday. Ranges are capped at 31 days.",
		Annotations: readOnly,
	}, s.listAwaitingFeedback)
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
		Name:        "list_news_topics",
		Description: "List the school's news topics in the school's order, with the slug list_news accepts.",
		Annotations: readOnly,
	}, s.listNewsTopics)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_news",
		Description: "List school news articles addressed to the parent, with their topics, optionally limited to one topic from list_news_topics.",
		Annotations: readOnly,
	}, s.listNews)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "search",
		Description: "Search Schoolbox for a keyword, most relevant first: news, events, class and folder pages, files, " +
			"and anything else the parent can open. Optionally limit to result types and a date range. " +
			"News results carry an articleId for get_article. Returns 20 results unless limit (at most 50) says otherwise.",
		Annotations: readOnly,
	}, s.search)
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
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_assessment",
		Description: "Read one assessment or homework task in full: title, subject, due date, the task description as " +
			"plain text, task files, the teacher's grading (mark, comment, annotated files), the mark scheme, the " +
			"rubric with every descriptor and the selected one flagged, and the submission history. Take " +
			"assessment_id from list_marked_work, list_awaiting_feedback, get_due_work or get_calendar. File urls open in a browser signed " +
			"in to Schoolbox; use get_assessment_file to read a file's content.",
		Annotations: readOnly,
	}, s.getAssessment)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_assessment_file",
		Description: "Download a file listed by get_assessment: a task sheet, a student submission or an annotated " +
			"feedback file. Images return as image content, text as text, and other files (PDF, Word) as an " +
			"embedded resource. Kami annotations open only in Schoolbox. Files over 10 MB are refused.",
		Annotations: readOnly,
	}, s.getAssessmentFile)
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
	AllDay   bool   `json:"allDay,omitzero"`
	Type     string `json:"type,omitempty"`
	Location string `json:"location,omitempty"`
	Author   string `json:"author,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Due work only: whether the student has ticked it off. Schoolbox sends
	// false on every other event, where it means nothing.
	Done *bool  `json:"completed,omitempty"`
	Link string `json:"link,omitempty"`
	// Due work only; matches assessmentId in list_marked_work and is what
	// get_assessment takes.
	AssessmentID *int `json:"assessmentId,omitempty"`
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
	events, err := s.events(ctx, student.ID, start, end)
	if err != nil {
		return nil, calendarOut{}, err
	}
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

// events fetches a student's calendar in start order, refusing ranges that
// Schoolbox would serve slowly or truncate.
func (s *service) events(ctx context.Context, studentID int, start, end time.Time) ([]schoolbox.Event, error) {
	// An hour of slack absorbs a daylight-saving change inside the range.
	if end.Sub(start) > maxCalendarDays*24*time.Hour+time.Hour {
		return nil, fmt.Errorf("range is longer than %d days; split it into smaller requests", maxCalendarDays)
	}
	events, err := s.client.Calendar(ctx, studentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	slices.SortStableFunc(events, func(x, y schoolbox.Event) int { return x.Start.Compare(y.Start) })
	return events, nil
}

type awaitingOut struct {
	Student string  `json:"student"`
	Period  period  `json:"period"`
	Count   int     `json:"count"`
	Items   []Event `json:"items"`
}

// feedbackLookback reaches back before the due-date range, because a teacher
// can publish feedback before the due date.
const feedbackLookback = maxCalendarDays

func (s *service) listAwaitingFeedback(ctx context.Context, _ *mcp.CallToolRequest, a rangeArgs) (*mcp.CallToolResult, awaitingOut, error) {
	student, err := s.resolveStudent(ctx, a.StudentID)
	if err != nil {
		return nil, awaitingOut{}, err
	}
	start, end, err := s.span(a, daysBefore(14), throughToday)
	if err != nil {
		return nil, awaitingOut{}, err
	}
	// Work due today can still be handed in, so the range stops at yesterday.
	if today := truncateDay(s.now()); end.After(today) {
		end = today
	}
	if !end.After(start) {
		return nil, awaitingOut{}, errors.New("from must be before today")
	}
	events, err := s.events(ctx, student.ID, start, end)
	if err != nil {
		return nil, awaitingOut{}, err
	}
	marked, err := s.feedback(ctx, student.ID, start.AddDate(0, 0, -feedbackLookback), truncateDay(s.now()).AddDate(0, 0, 1))
	if err != nil {
		return nil, awaitingOut{}, err
	}
	seen := make(map[int]bool, len(marked))
	for i := range marked {
		seen[marked[i].AssessmentID] = true
	}
	out := awaitingOut{Student: student.FullName, Period: periodOf(start, end), Items: []Event{}}
	for i := range events {
		e := &events[i]
		if !e.IsDueWork() || !e.Start.Before(end) || seen[e.AssessmentID] {
			continue
		}
		out.Items = append(out.Items, s.event(e))
	}
	out.Count = len(out.Items)
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
		Link:     s.link(e.Path),
	}
	if !e.End.IsZero() {
		out.End = e.End.In(loc).Format(layout)
	}
	if e.IsDueWork() {
		out.Title = cmp.Or(e.Meta.Title, e.Title)
		out.Subject = e.Meta.FolderName
		out.WorkType = e.Meta.WorkType
		out.Done = new(e.Meta.Completed)
		if e.AssessmentID != 0 {
			out.AssessmentID = new(e.AssessmentID)
		}
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
	AssessmentID int    `json:"assessmentId"`
	FeedbackID   int    `json:"feedbackId"`
	Title        string `json:"title"`
	Subject      string `json:"subject"`
	WorkType     string `json:"workType,omitempty"`
	// Set only when the teacher weighted the assessment; Schoolbox sends 0 otherwise.
	Weight  *float64 `json:"weight,omitempty"`
	Due     string   `json:"due,omitempty"`
	Marked  string   `json:"marked"`
	Teacher string   `json:"teacher"`
	Mark    string   `json:"mark"`
	Percent *float64 `json:"percent,omitempty"`
	Grade   string   `json:"grade,omitempty"`
	Status  string   `json:"status,omitempty"`
	// Percent is a stand-in for the grade (C=70, B=85), not an earned score.
	GradeBand bool   `json:"gradeBand,omitzero"`
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
			Title: w.Title, Subject: w.Subject, WorkType: w.WorkType, Weight: weightOf(w), Marked: marked,
			Score: grades.Score{Percent: w.Percent, Grade: w.Grade, Status: w.Status, GradeBand: w.GradeBand},
		})
	}
	return nil, statsOut{Student: student.FullName, Period: periodOf(start, end), Summary: grades.Summarise(results)}, nil
}

func weightOf(w *MarkedWork) float64 {
	if w.Weight == nil {
		return 0
	}
	return *w.Weight
}

// positive returns a pointer to v, or nil when v is not above zero.
func positive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
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
			Weight:       positive(f.Assessment.Weight),
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

type NewsTopic struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type topicsOut struct {
	Topics []NewsTopic `json:"topics"`
}

func (s *service) listNewsTopics(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, topicsOut, error) {
	topics, err := s.newsTopics(ctx)
	if err != nil {
		return nil, topicsOut{}, err
	}
	out := topicsOut{Topics: make([]NewsTopic, 0, len(topics))}
	for _, t := range topics {
		out.Topics = append(out.Topics, NewsTopic{Name: t.Name, Slug: t.Slug()})
	}
	return nil, out, nil
}

func (s *service) newsTopics(ctx context.Context) ([]schoolbox.Topic, error) {
	topics, err := s.client.NewsTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("news topics: %w", err)
	}
	slices.SortStableFunc(topics, func(x, y schoolbox.Topic) int { return cmp.Compare(x.Sequence, y.Sequence) })
	return topics, nil
}

// resolveTopic matches a slug or name, ignoring case, against the school's topics.
func (s *service) resolveTopic(ctx context.Context, want string) (schoolbox.Topic, error) {
	topics, err := s.newsTopics(ctx)
	if err != nil {
		return schoolbox.Topic{}, err
	}
	slugs := make([]string, 0, len(topics))
	for _, t := range topics {
		if strings.EqualFold(t.Slug(), want) || strings.EqualFold(t.Name, want) {
			return t, nil
		}
		slugs = append(slugs, t.Slug())
	}
	return schoolbox.Topic{}, fmt.Errorf("no news topic %q; choose one of: %s", want, strings.Join(slugs, ", "))
}

// hasTopic matches on slug or name, so a slug derived differently from the
// server's still finds the topic by its name.
func hasTopic(n *schoolbox.ArticleSummary, t schoolbox.Topic) bool {
	return slices.ContainsFunc(n.Topics, func(at schoolbox.ArticleTopic) bool {
		return at.Slug == t.Slug() || strings.EqualFold(at.Name, t.Name)
	})
}

type newsArgs struct {
	Topic string `json:"topic,omitempty" jsonschema:"optional topic slug or name from list_news_topics"`
}

type NewsItem struct {
	ID        int      `json:"id"`
	Title     string   `json:"title"`
	Blurb     string   `json:"blurb,omitempty"`
	Author    string   `json:"author"`
	Published string   `json:"published"`
	Unread    bool     `json:"unread,omitzero"`
	Topics    []string `json:"topics,omitempty"`
}

type newsOut struct {
	Articles []NewsItem `json:"articles"`
}

func (s *service) listNews(ctx context.Context, _ *mcp.CallToolRequest, a newsArgs) (*mcp.CallToolResult, newsOut, error) {
	var topic *schoolbox.Topic
	if a.Topic != "" {
		t, err := s.resolveTopic(ctx, a.Topic)
		if err != nil {
			return nil, newsOut{}, err
		}
		topic = &t
	}
	list, err := s.client.News(ctx)
	if err != nil {
		return nil, newsOut{}, fmt.Errorf("news: %w", err)
	}
	out := newsOut{Articles: []NewsItem{}}
	loc := s.now().Location()
	for i := range list {
		n := &list[i]
		if topic != nil && !hasTopic(n, *topic) {
			continue
		}
		item := NewsItem{
			ID: n.ID, Title: n.Title, Blurb: htmlToText(n.Blurb), Author: n.Author.FullName,
			Published: n.PublishAt.In(loc).Format(time.DateOnly), Unread: n.ViewedAt == nil,
		}
		for _, t := range n.Topics {
			item.Topics = append(item.Topics, t.Name)
		}
		out.Articles = append(out.Articles, item)
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

const (
	defaultSearchResults = 20
	maxSearchResults     = 50
)

type searchArgs struct {
	Query string   `json:"query" jsonschema:"keyword or phrase, at least 2 characters"`
	Types []string `json:"types,omitempty" jsonschema:"optional result types, e.g. news, event, page, file, assessment, user.staff"`
	From  string   `json:"from,omitempty" jsonschema:"optional first day, YYYY-MM-DD; its meaning varies by result type"`
	To    string   `json:"to,omitempty" jsonschema:"optional last day, YYYY-MM-DD"`
	Limit int      `json:"limit,omitempty" jsonschema:"maximum results, 1 to 50; default 20"`
}

type SearchHit struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Date  string `json:"date,omitempty"`
	// The folders the result sits in, e.g. "Year 9 / Camps".
	Where   string `json:"where,omitempty"`
	Meta    string `json:"meta,omitempty"`
	Summary string `json:"summary,omitempty"`
	Link    string `json:"link,omitempty"`
	// News results only; pass to get_article.
	ArticleID *int `json:"articleId,omitempty"`
}

type searchOut struct {
	Query   string      `json:"query"`
	Count   int         `json:"count"`
	More    bool        `json:"more"`
	Results []SearchHit `json:"results"`
}

func (a searchArgs) filter() (schoolbox.SearchFilter, int, error) {
	f := schoolbox.SearchFilter{Types: a.Types, From: a.From, To: a.To}
	if utf8.RuneCountInString(strings.TrimSpace(a.Query)) < 2 {
		return f, 0, errors.New("query must be at least 2 characters")
	}
	for _, t := range a.Types {
		if !slices.Contains(schoolbox.SearchTypes, t) {
			return f, 0, fmt.Errorf("unknown type %q; choose from: %s", t, strings.Join(schoolbox.SearchTypes, ", "))
		}
	}
	for name, d := range map[string]string{"from": a.From, "to": a.To} {
		if _, err := time.Parse(time.DateOnly, d); d != "" && err != nil {
			return f, 0, fmt.Errorf("%s: want YYYY-MM-DD: %w", name, err)
		}
	}
	limit := cmp.Or(a.Limit, defaultSearchResults)
	if limit < 1 || limit > maxSearchResults {
		return f, 0, fmt.Errorf("limit must be between 1 and %d", maxSearchResults)
	}
	return f, limit, nil
}

func (s *service) search(ctx context.Context, _ *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, searchOut, error) {
	filter, limit, err := a.filter()
	if err != nil {
		return nil, searchOut{}, err
	}
	query := strings.TrimSpace(a.Query)
	out := searchOut{Query: query, Results: []SearchHit{}}
	for r, err := range s.client.Search(ctx, query, filter) {
		if err != nil {
			return nil, searchOut{}, fmt.Errorf("search: %w", err)
		}
		out.Results = append(out.Results, s.searchHit(&r))
		// Stop without fetching another page, so More reports that results
		// may continue rather than that they do.
		if len(out.Results) == limit {
			out.More = true
			break
		}
	}
	out.Count = len(out.Results)
	return nil, out, nil
}

func (s *service) searchHit(r *schoolbox.SearchResult) SearchHit {
	hit := SearchHit{
		Type:    r.Type,
		Title:   r.Title,
		Meta:    r.Meta,
		Summary: htmlToText(r.Description),
	}
	if !r.Date.IsZero() {
		hit.Date = r.Date.In(s.now().Location()).Format(time.DateOnly)
	}
	where := make([]string, 0, len(r.Context))
	for _, c := range r.Context {
		where = append(where, c.Name)
	}
	hit.Where = strings.Join(where, " / ")
	if r.URL != "" {
		// Drop the ref=search tracking parameter.
		if u, err := url.Parse(r.URL); err == nil {
			q := u.Query()
			q.Del("ref")
			u.RawQuery = q.Encode()
			hit.Link = s.link(u.String())
		}
	}
	if r.Type == "news" {
		if id, err := strconv.Atoi(r.ID); err == nil {
			hit.ArticleID = &id
		}
	}
	return hit
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
	source := fmt.Sprintf("article %d", a.ArticleID)
	return &mcp.CallToolResult{Content: fileContent(file.Name, file.Mime, contentType, s.link(file.Links.Fetch), source, body)}, nil, nil
}

// fileContent describes a downloaded file and returns it as content suited to
// its type: images as images, UTF-8 text as text, and anything else as an
// embedded resource. The response's content type wins over the listed one
// unless it is the generic octet-stream.
func fileContent(name, listedMIME, contentType, uri, source string, body []byte) []mcp.Content {
	mime := listedMIME
	if mt, _, err := gomime.ParseMediaType(contentType); err == nil && mt != "application/octet-stream" {
		mime = mt
	}
	summary := &mcp.TextContent{Text: fmt.Sprintf("%s (%s, %d bytes) from %s", name, mime, len(body), source)}
	var content mcp.Content
	switch {
	case strings.HasPrefix(mime, "image/"):
		content = &mcp.ImageContent{Data: body, MIMEType: mime}
	case strings.HasPrefix(mime, "text/") && utf8.Valid(body):
		content = &mcp.TextContent{Text: string(body)}
	default:
		content = &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: uri, MIMEType: mime, Blob: body}}
	}
	return []mcp.Content{summary, content}
}
