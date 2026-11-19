package schoolbox

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// The assessment page has no JSON alternative, so it is scraped. Every
// selector was observed on Schoolbox 26.1.17. data-test hooks, <time datetime>
// and Vue props are what Schoolbox's own tests and front end rely on; the
// Foundation classes are stable but cosmetic; text and icon names are the most
// likely to drift.
var (
	selContent     = cascadia.MustCompile("#content")
	selTitle       = cascadia.MustCompile("h1")
	selMeta        = cascadia.MustCompile("ul.meta")
	selMetaItem    = cascadia.MustCompile("ul.meta > li")
	selMetaIcon    = cascadia.MustCompile("sbx-icon[name]")
	selTime        = cascadia.MustCompile("time[datetime]")
	selSubject     = cascadia.MustCompile("ul.breadcrumb > li:nth-child(2) a")
	selDescription = cascadia.MustCompile("article.content")
	selTaskFile    = cascadia.MustCompile(`ul.grid .card.attachment a[href^="/storage/fetch.php"]`)
	selMimeIcon    = cascadia.MustCompile(`img[src^="/ajax/mime/"]`)
	selFileSize    = cascadia.MustCompile("p.meta")
	selFirstPara   = cascadia.MustCompile("p")

	selGrading      = cascadia.MustCompile(`[data-test="assessment-feedback-summary"]`)
	selHistory      = cascadia.MustCompile(`[data-test="assessment-submission-history"]`)
	selFeedbackItem = cascadia.MustCompile("ul.reply > li")
	selHistoryItem  = cascadia.MustCompile("ul.reply > li, ul.threaded > li")
	selAuthorCard   = cascadia.MustCompile(".list-item > .card:not(.attachment)")
	selAuthorLink   = cascadia.MustCompile(`a[href^="/search/user/"]`)
	selCardMeta     = cascadia.MustCompile(".meta")
	selMark         = cascadia.MustCompile(`[data-test="assessment-feedback-mark"] span`)
	selComment      = cascadia.MustCompile("blockquote")
	selFeedbackFile = cascadia.MustCompile("ul.information-list .card.attachment a[href]")
	selSubmission   = cascadia.MustCompile(`a[data-test="assessment-summary-files-list-file"]`)
	selRubric       = cascadia.MustCompile("rubric-readonly")

	selLoginForm = cascadia.MustCompile(`form[action*="login"], input[type="password"]`)
)

// Icons that label the ul.meta items. The work-type icon varies with the
// assessment type, so it is whatever is left over.
const (
	iconDue         = "calendar"
	iconSubmissions = "upload"
	iconStudent     = "staff-students"
)

// feedbackTimeLayout is the plain-text feedback timestamp, in school time.
const feedbackTimeLayout = "Jan 2, 2006 3:04pm"

var (
	// ErrLoginRequired means Schoolbox sent the login page instead of the
	// requested one, which happens when the token is rejected.
	ErrLoginRequired = errors.New("schoolbox rejected the token; it may have expired")
	// ErrUnexpectedPage means an HTML page lacked an element the scraper
	// anchors on, most likely after a Schoolbox upgrade changed the template.
	ErrUnexpectedPage = errors.New("unexpected page layout")
)

// File kinds on an assessment page.
const (
	FileTask       = "task"
	FileSubmission = "submission"
	FileAnnotation = "annotation"
	// FileKami links to the Kami viewer, not a file.
	FileKami = "kami"
)

// AssessmentFile is a file linked from an assessment page.
type AssessmentFile struct {
	Name string
	Kind string
	// Size as the page shows it, e.g. "476.79 KB"; empty when not shown.
	Size string
	// MIME type from the page's file icon, when there is one.
	MIME string
	// Site-relative link.
	Link string
}

// Retrievable reports whether Link is a file rather than a viewer page.
func (f AssessmentFile) Retrievable() bool { return f.Kind != FileKami }

// AssessmentFeedback is one teacher feedback entry.
type AssessmentFeedback struct {
	// "Feedback" or "Resubmission required".
	Action  string
	Teacher string
	At      time.Time
	// Mark as shown; empty when the entry has none.
	Mark        string
	CommentHTML string
	Files       []AssessmentFile
}

// HistoryEntry is a feedback entry, a student submission or the student's
// self-reflection, in page order.
type HistoryEntry struct {
	// HistoryFeedback, HistorySubmission or HistorySelfReflection.
	Kind     string
	Feedback *AssessmentFeedback `json:",omitempty"`
	// Submission and self-reflection fields. A self-reflection byline names
	// no one, so By is empty for it.
	By    string           `json:",omitempty"`
	At    time.Time        `json:",omitzero"`
	Files []AssessmentFile `json:",omitempty"`
	// CommentHTML is the self-reflection comment, when the student wrote one.
	CommentHTML string `json:",omitempty"`
}

const (
	HistoryFeedback   = "feedback"
	HistorySubmission = "submission"
	// HistorySelfReflection is the student's own reflection on the work,
	// shown in the same layout as teacher feedback.
	HistorySelfReflection = "self-reflection"
)

// MarkScheme is how the assessment is marked: "raw" out of TotalMark,
// "percent", or "letter" grades in Bands.
type MarkScheme struct {
	Mode      string
	TotalMark *float64    `json:",omitempty"`
	Bands     []GradeBand `json:",omitempty"`
}

// GradeBand is one letter grade: marks from LowerBound percent earn Caption,
// which Schoolbox counts as RawGrade percent.
type GradeBand struct {
	LowerBound float64 `json:"lowerBound"`
	RawGrade   float64 `json:"rawGrade"`
	Caption    string  `json:"caption"`
}

var errNoMarkScheme = errors.New("no mark scheme")

// decodeMarkScheme reads the :assessment-mark-scheme prop, whose conf is an
// object for raw marks and an array of bands for letter grades.
func decodeMarkScheme(raw []byte) (*MarkScheme, error) {
	var wire struct {
		Mode string         `json:"mode"`
		Conf jsontext.Value `json:"conf"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	if wire.Mode == "" {
		return nil, errNoMarkScheme
	}
	ms := &MarkScheme{Mode: wire.Mode}
	switch wire.Conf.Kind() {
	case '[':
		if err := json.Unmarshal(wire.Conf, &ms.Bands); err != nil {
			return nil, err
		}
	case '{':
		var conf struct {
			TotalMark *float64 `json:"totalMark"`
		}
		if err := json.Unmarshal(wire.Conf, &conf); err != nil {
			return nil, err
		}
		ms.TotalMark = conf.TotalMark
	}
	return ms, nil
}

// Rubric is one marked rubric grid. Names and descriptor text are HTML.
type Rubric struct {
	Title    string
	Levels   []string
	Criteria []RubricCriterion
	// Mark is the overall human-readable mark, when the grid is marked.
	Mark string
}

// RubricCriterion is a rubric capability: a group of indicators.
type RubricCriterion struct {
	Name       string
	Mark       string
	Indicators []RubricIndicator
}

// RubricIndicator is one row of the grid, with a descriptor per level.
type RubricIndicator struct {
	Name        string
	Descriptors []RubricDescriptor
}

type RubricDescriptor struct {
	// Level is the name of the column, matched by position.
	Level    string
	Text     string
	Points   *float64
	Selected bool
}

// AssessmentPage is an assessment as a parent sees it. HTML fields hold
// teacher-authored markup for the caller to flatten.
type AssessmentPage struct {
	Title       string
	Subject     string
	WorkType    string
	Due         time.Time
	Submissions string

	DescriptionHTML string
	TaskFiles       []AssessmentFile
	Grading         []AssessmentFeedback
	MarkScheme      *MarkScheme
	Rubrics         []Rubric
	History         []HistoryEntry
	// Warnings name parts of the page that could not be read.
	Warnings []string
}

// AssessmentPath is the parent's view of a student's assessment.
func AssessmentPath(assessmentID, studentID int) string {
	return fmt.Sprintf("/learning/assessments/%d/%d", assessmentID, studentID)
}

// Assessment fetches and scrapes one assessment page. Feedback timestamps are
// plain local times and are read in loc, the school's time zone.
//
// The page accepts the bearer token. A student who is not the parent's child
// gets a 401 page (ErrPermission) and an unknown assessment a 404 page
// (ErrNotFound). Without the student segment the URL answers 303 to the
// enrolled child's page. That redirect is not used: callers already hold the
// student id, the client does not follow redirects, so it would cost a second
// request, and which sibling it picks when siblings share an assessment is
// unknown. Visible due dates are relative ("Friday"), so only <time datetime>
// is read.
func (c *Client) Assessment(ctx context.Context, assessmentID, studentID int, loc *time.Location) (AssessmentPage, error) {
	path := AssessmentPath(assessmentID, studentID)
	resp, err := c.send(ctx, path, nil, "text/html")
	if err != nil {
		return AssessmentPage{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	doc, err := html.Parse(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return AssessmentPage{}, fmt.Errorf("GET %s: parse: %w", path, err)
	}
	page, err := parseAssessment(doc, studentID, loc)
	if err != nil {
		return AssessmentPage{}, fmt.Errorf("GET %s: %w", path, err)
	}
	return page, nil
}

func parseAssessment(doc *html.Node, studentID int, loc *time.Location) (AssessmentPage, error) {
	content := selContent.MatchFirst(doc)
	if content == nil || selMeta.MatchFirst(content) == nil {
		if selLoginForm.MatchFirst(doc) != nil {
			return AssessmentPage{}, ErrLoginRequired
		}
		if content == nil {
			return AssessmentPage{}, fmt.Errorf("%w: no #content", ErrUnexpectedPage)
		}
		return AssessmentPage{}, fmt.Errorf("%w: no ul.meta in #content", ErrUnexpectedPage)
	}
	title := selTitle.MatchFirst(content)
	if title == nil {
		return AssessmentPage{}, fmt.Errorf("%w: no h1 in #content", ErrUnexpectedPage)
	}
	p := AssessmentPage{Title: text(title), Subject: text(selSubject.MatchFirst(content))}
	if err := p.readMeta(content); err != nil {
		return AssessmentPage{}, err
	}
	if d := selDescription.MatchFirst(content); d != nil {
		p.DescriptionHTML = strings.TrimSpace(innerHTML(d))
	}
	for _, a := range selTaskFile.MatchAll(content) {
		p.TaskFiles = append(p.TaskFiles, fileRef(a, FileTask))
	}
	if g := selGrading.MatchFirst(content); g != nil {
		for _, li := range selFeedbackItem.MatchAll(g) {
			p.Grading = append(p.Grading, feedbackEntry(li, loc))
		}
		p.readRubric(g)
	}
	for _, section := range selHistory.MatchAll(content) {
		for _, li := range selHistoryItem.MatchAll(section) {
			p.History = append(p.History, historyEntry(li, studentID, loc))
		}
	}
	return p, nil
}

func (p *AssessmentPage) readMeta(content *html.Node) error {
	for _, li := range selMetaItem.MatchAll(content) {
		icon := selMetaIcon.MatchFirst(li)
		if icon == nil {
			continue
		}
		switch attr(icon, "name") {
		case iconDue:
			t := selTime.MatchFirst(li)
			if t == nil {
				continue
			}
			due, err := time.Parse(time.RFC3339, attr(t, "datetime"))
			if err != nil {
				return fmt.Errorf("%w: due date: %w", ErrUnexpectedPage, err)
			}
			p.Due = due
		case iconSubmissions:
			p.Submissions = text(li)
		case iconStudent:
		default:
			p.WorkType = text(li)
		}
	}
	return nil
}

// readRubric decodes the rubric-readonly Vue props of the first grading entry.
// A rubric that cannot be decoded becomes a warning, not an error.
func (p *AssessmentPage) readRubric(grading *html.Node) {
	el := selRubric.MatchFirst(grading)
	if el == nil {
		return
	}
	if raw := attr(el, ":assessment-mark-scheme"); raw != "" {
		ms, err := decodeMarkScheme([]byte(raw))
		switch {
		case err == nil:
			p.MarkScheme = ms
		case !errors.Is(err, errNoMarkScheme):
			p.Warnings = append(p.Warnings, "mark scheme could not be read")
		}
	}
	raw := attr(el, ":rubric-marked-grids-payload")
	if raw == "" {
		return
	}
	rubrics, err := decodeRubrics([]byte(raw))
	if err != nil {
		p.Warnings = append(p.Warnings, "rubric could not be read")
		return
	}
	p.Rubrics = rubrics
}

type rubricWire struct {
	Rubric struct {
		Title  string `json:"title"`
		Levels []struct {
			Name string `json:"name"`
		} `json:"levels"`
		Capabilities []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Indicators []struct {
				ID          int    `json:"id"`
				Name        string `json:"name"`
				Descriptors []struct {
					ID          int    `json:"id"`
					Description string `json:"description"`
					Value       struct {
						Max *float64 `json:"max"`
					} `json:"value"`
				} `json:"descriptors"`
			} `json:"indicators"`
		} `json:"capabilities"`
	} `json:"rubric"`
	MarkedGrid *struct {
		HumanReadableMark jsontext.Value `json:"humanReadableMark"`
		Capabilities      []struct {
			ID                int            `json:"id"`
			HumanReadableMark jsontext.Value `json:"humanReadableMark"`
			Indicators        []struct {
				ID           int  `json:"id"`
				DescriptorID *int `json:"descriptorId"`
			} `json:"indicators"`
		} `json:"capabilities"`
	} `json:"markedGrid"`
}

func decodeRubrics(raw []byte) ([]Rubric, error) {
	var wire []rubricWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	var out []Rubric
	for i := range wire {
		out = append(out, wire[i].rubric())
	}
	return out, nil
}

// rubric maps the marked grid onto the rubric: each marked indicator names its
// chosen descriptorId, and descriptors align to levels by position. The
// marked-rubric fixture is an anonymised capture of a real marked grid.
func (w *rubricWire) rubric() Rubric {
	selected := map[int]int{}    // indicator id → descriptor id
	capMarks := map[int]string{} // capability id → mark
	r := Rubric{Title: w.Rubric.Title}
	if g := w.MarkedGrid; g != nil {
		r.Mark = markText(g.HumanReadableMark)
		for _, c := range g.Capabilities {
			capMarks[c.ID] = markText(c.HumanReadableMark)
			for _, ind := range c.Indicators {
				if ind.DescriptorID != nil {
					selected[ind.ID] = *ind.DescriptorID
				}
			}
		}
	}
	for _, l := range w.Rubric.Levels {
		r.Levels = append(r.Levels, l.Name)
	}
	for _, c := range w.Rubric.Capabilities {
		crit := RubricCriterion{Name: c.Name, Mark: capMarks[c.ID]}
		for _, ind := range c.Indicators {
			row := RubricIndicator{Name: ind.Name}
			for i, d := range ind.Descriptors {
				desc := RubricDescriptor{Text: d.Description, Points: d.Value.Max}
				if i < len(r.Levels) {
					desc.Level = r.Levels[i]
				}
				if sel, ok := selected[ind.ID]; ok && sel == d.ID {
					desc.Selected = true
				}
				row.Descriptors = append(row.Descriptors, desc)
			}
			crit.Indicators = append(crit.Indicators, row)
		}
		r.Criteria = append(r.Criteria, crit)
	}
	return r
}

// markText renders a human-readable mark that may be a string, a number or null.
func markText(v jsontext.Value) string {
	switch v.Kind() {
	case '"':
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	case '0':
		return string(v)
	}
	return ""
}

func feedbackEntry(li *html.Node, loc *time.Location) AssessmentFeedback {
	var f AssessmentFeedback
	f.Action, f.Teacher, f.At = byline(li, loc)
	f.Mark = text(selMark.MatchFirst(li))
	if q := selComment.MatchFirst(li); q != nil {
		f.CommentHTML = strings.TrimSpace(innerHTML(q))
	}
	for _, a := range selFeedbackFile.MatchAll(li) {
		kind := FileAnnotation
		if strings.HasPrefix(attr(a, "href"), "/learning/kami/") {
			kind = FileKami
		}
		f.Files = append(f.Files, fileRef(a, kind))
	}
	return f
}

// historyEntry reads one history item. A self-reflection uses the feedback
// layout and its byline is plain text ("Self-reflection"), so it is told apart
// by its author: the profile link points at the student, not a teacher.
func historyEntry(li *html.Node, studentID int, loc *time.Location) HistoryEntry {
	if selFeedbackItem.Match(li) {
		if authorID(li) == studentID {
			e := HistoryEntry{Kind: HistorySelfReflection}
			_, _, e.At = byline(li, loc)
			if q := selComment.MatchFirst(li); q != nil {
				e.CommentHTML = strings.TrimSpace(innerHTML(q))
			}
			return e
		}
		f := feedbackEntry(li, loc)
		return HistoryEntry{Kind: HistoryFeedback, Feedback: &f}
	}
	e := HistoryEntry{Kind: HistorySubmission}
	_, e.By, e.At = byline(li, loc)
	for _, a := range selSubmission.MatchAll(li) {
		f := fileRef(a, FileSubmission)
		f.Link = submissionDownload(f.Link)
		e.Files = append(e.Files, f)
	}
	return e
}

// submissionDownload returns the link that serves a submission's content.
// Uploaded files link to …/files/N/download, but text typed into the online
// editor links to …/files/N, a preview page; its content is at the same path
// with /download appended.
func submissionDownload(link string) string {
	u, err := url.Parse(link)
	if err != nil || strings.HasSuffix(u.Path, "/download") {
		return link
	}
	u.Path += "/download"
	return u.String()
}

// authorID is the user id in the entry's profile link, or 0 without one.
func authorID(li *html.Node) int {
	card := selAuthorCard.MatchFirst(li)
	if card == nil {
		return 0
	}
	a := selAuthorLink.MatchFirst(card)
	if a == nil {
		return 0
	}
	id, err := strconv.Atoi(strings.TrimPrefix(attr(a, "href"), "/search/user/"))
	if err != nil {
		return 0
	}
	return id
}

// byline reads "Feedback by Name", "Resubmission required by Name" or
// "Submission by Name" and the timestamp beneath it.
func byline(li *html.Node, loc *time.Location) (action, name string, at time.Time) {
	card := selAuthorCard.MatchFirst(li)
	if card == nil {
		return "", "", at
	}
	if p := selFirstPara.MatchFirst(card); p != nil {
		action, name, _ = strings.Cut(ownText(p), " by ")
	}
	if m := selCardMeta.MatchFirst(card); m != nil {
		at, _ = time.ParseInLocation(feedbackTimeLayout, text(m), loc)
	}
	return action, name, at
}

func fileRef(a *html.Node, kind string) AssessmentFile {
	f := AssessmentFile{Kind: kind, Link: attr(a, "href"), Name: attr(a, "title")}
	if kind == FileTask {
		f.Size = text(selFileSize.MatchFirst(a))
		// The link's name parameter keeps the extension the title drops.
		if u, err := url.Parse(f.Link); err == nil {
			if n := u.Query().Get("name"); n != "" {
				f.Name = n
			}
		}
	}
	if f.Name == "" {
		f.Name = text(selFirstPara.MatchFirst(a))
	}
	if img := selMimeIcon.MatchFirst(a); img != nil {
		f.MIME = strings.TrimPrefix(attr(img, "src"), "/ajax/mime/")
	}
	return f
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// text is the node's text content with whitespace collapsed.
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ownText is the node's direct text children with whitespace collapsed.
func ownText(n *html.Node) string {
	var b strings.Builder
	for c := range n.ChildNodes() {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func innerHTML(n *html.Node) string {
	var b strings.Builder
	for c := range n.ChildNodes() {
		_ = html.Render(&b, c)
	}
	return b.String()
}
