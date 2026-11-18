package server

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jamestelfer/unboxed/internal/grades"
	"github.com/jamestelfer/unboxed/internal/schoolbox"
)

type assessmentArgs struct {
	StudentID    int `json:"student_id,omitempty" jsonschema:"student id from list_students; may be omitted when the parent has one child"`
	AssessmentID int `json:"assessment_id" jsonschema:"assessment id from list_marked_work, list_awaiting_feedback, get_due_work or get_calendar"`
}

type assessmentOut struct {
	AssessmentID int    `json:"assessmentId"`
	Student      string `json:"student"`
	Title        string `json:"title"`
	Subject      string `json:"subject,omitempty"`
	WorkType     string `json:"workType,omitempty"`
	// RFC 3339 in school time.
	Due string `json:"due,omitempty"`
	// Submission status, e.g. "This assessment no longer accepts submissions."
	Submissions string         `json:"submissions,omitempty"`
	Description string         `json:"description,omitempty"`
	TaskFiles   []FileRef      `json:"taskFiles,omitempty"`
	Grading     []Feedback     `json:"grading,omitempty"`
	MarkScheme  *MarkScheme    `json:"markScheme,omitempty"`
	Rubric      []Rubric       `json:"rubric,omitempty"`
	History     []HistoryEntry `json:"history,omitempty"`
	Link        string         `json:"link"`
	Warnings    []string       `json:"warnings,omitempty"`
}

type Feedback struct {
	// "Feedback" or "Resubmission required".
	Action  string `json:"action"`
	Teacher string `json:"teacher"`
	At      string `json:"at,omitempty"`
	Mark    string `json:"mark,omitempty"`
	grades.Score
	Comment string    `json:"comment,omitempty"`
	Files   []FileRef `json:"files,omitempty"`
}

type FileRef struct {
	Name string `json:"name"`
	// task, submission, annotation or kami.
	Kind string `json:"kind"`
	// Size as the page shows it ("476.79 KB"); empty for submissions and Kami links.
	Size string `json:"size,omitempty"`
	MIME string `json:"mime,omitempty"`
	// Absolute URL for direct download in a browser signed in to Schoolbox.
	URL string `json:"url"`
	// False for Kami links, which are viewer pages rather than files.
	Retrievable bool `json:"retrievable"`
}

type MarkScheme struct {
	// raw, percent or letter.
	Mode      string   `json:"mode"`
	TotalMark *float64 `json:"totalMark,omitempty"`
	Bands     []Band   `json:"bands,omitempty"`
}

// Band is a letter grade awarded from LowerBound percent.
type Band struct {
	Caption    string  `json:"caption"`
	LowerBound float64 `json:"lowerBound"`
}

type Rubric struct {
	Title    string      `json:"title"`
	Levels   []string    `json:"levels"`
	Criteria []Criterion `json:"criteria"`
	Mark     string      `json:"mark,omitempty"`
}

type Criterion struct {
	Name       string      `json:"name"`
	Mark       string      `json:"mark,omitempty"`
	Indicators []Indicator `json:"indicators"`
}

type Indicator struct {
	Name        string       `json:"name"`
	Descriptors []Descriptor `json:"descriptors"`
}

type Descriptor struct {
	Level    string   `json:"level,omitempty"`
	Text     string   `json:"text"`
	Points   *float64 `json:"points,omitempty"`
	Selected bool     `json:"selected,omitzero"`
}

// HistoryEntry is a feedback entry or a student submission, in page order.
type HistoryEntry struct {
	// feedback or submission.
	Kind string `json:"kind"`
	// Teacher for feedback, student for a submission.
	By     string `json:"by"`
	At     string `json:"at,omitempty"`
	Action string `json:"action,omitempty"`
	Mark   string `json:"mark,omitempty"`
	grades.Score
	Comment string    `json:"comment,omitempty"`
	Files   []FileRef `json:"files,omitempty"`
}

func (s *service) getAssessment(ctx context.Context, _ *mcp.CallToolRequest, a assessmentArgs) (*mcp.CallToolResult, assessmentOut, error) {
	student, page, err := s.assessment(ctx, a)
	if err != nil {
		return nil, assessmentOut{}, err
	}
	out := s.assessmentOut(a.AssessmentID, student, &page)
	text, err := json.Marshal(out)
	if err != nil {
		return nil, assessmentOut{}, fmt.Errorf("encode assessment: %w", err)
	}
	// Setting Content suppresses the SDK's default JSON text block, so it is
	// written here, followed by a link per retrievable file.
	content := []mcp.Content{&mcp.TextContent{Text: string(text)}}
	for _, f := range s.files(pageFiles(&page)) {
		if !f.Retrievable {
			continue
		}
		content = append(content, &mcp.ResourceLink{URI: f.URL, Name: f.Name, MIMEType: f.MIME, Size: parseSize(f.Size)})
	}
	return &mcp.CallToolResult{Content: content}, out, nil
}

// assessment resolves the student and fetches the page, turning client errors
// into messages a parent can act on.
func (s *service) assessment(ctx context.Context, a assessmentArgs) (schoolbox.Person, schoolbox.AssessmentPage, error) {
	student, err := s.resolveStudent(ctx, a.StudentID)
	if err != nil {
		return student, schoolbox.AssessmentPage{}, err
	}
	if a.AssessmentID <= 0 {
		return student, schoolbox.AssessmentPage{}, errors.New("assessment_id is required; take it from list_marked_work, list_awaiting_feedback, get_due_work or get_calendar")
	}
	page, err := s.client.Assessment(ctx, a.AssessmentID, student.ID, s.now().Location())
	switch {
	case err == nil:
		return student, page, nil
	case errors.Is(err, schoolbox.ErrPermission):
		return student, page, fmt.Errorf("assessment %d is not visible for student %d (%s)", a.AssessmentID, student.ID, student.FullName)
	case errors.Is(err, schoolbox.ErrNotFound):
		return student, page, fmt.Errorf("no assessment %d for student %d (%s)", a.AssessmentID, student.ID, student.FullName)
	}
	return student, page, fmt.Errorf("assessment %d: %w", a.AssessmentID, err)
}

func (s *service) assessmentOut(id int, student schoolbox.Person, p *schoolbox.AssessmentPage) assessmentOut {
	out := assessmentOut{
		AssessmentID: id,
		Student:      student.FullName,
		Title:        p.Title,
		Subject:      p.Subject,
		WorkType:     p.WorkType,
		Due:          s.timestamp(p.Due),
		Submissions:  p.Submissions,
		Description:  htmlToText(p.DescriptionHTML),
		TaskFiles:    s.files(p.TaskFiles),
		Link:         s.link(schoolbox.AssessmentPath(id, student.ID)),
		Warnings:     p.Warnings,
	}
	for i := range p.Grading {
		out.Grading = append(out.Grading, s.feedbackOut(&p.Grading[i]))
	}
	if ms := p.MarkScheme; ms != nil {
		out.MarkScheme = &MarkScheme{Mode: ms.Mode, TotalMark: ms.TotalMark}
		for _, b := range ms.Bands {
			out.MarkScheme.Bands = append(out.MarkScheme.Bands, Band{Caption: b.Caption, LowerBound: b.LowerBound})
		}
	}
	for i := range p.Rubrics {
		out.Rubric = append(out.Rubric, rubricOut(&p.Rubrics[i]))
	}
	for i := range p.History {
		out.History = append(out.History, s.historyOut(&p.History[i]))
	}
	return out
}

func (s *service) feedbackOut(f *schoolbox.AssessmentFeedback) Feedback {
	return Feedback{
		Action:  f.Action,
		Teacher: f.Teacher,
		At:      s.timestamp(f.At),
		Mark:    f.Mark,
		Score:   grades.Parse(f.Mark, nil, nil),
		Comment: htmlToText(f.CommentHTML),
		Files:   s.files(f.Files),
	}
}

func (s *service) historyOut(e *schoolbox.HistoryEntry) HistoryEntry {
	if e.Kind == schoolbox.HistoryFeedback && e.Feedback != nil {
		f := s.feedbackOut(e.Feedback)
		return HistoryEntry{
			Kind: e.Kind, By: f.Teacher, At: f.At, Action: f.Action,
			Mark: f.Mark, Score: f.Score, Comment: f.Comment, Files: f.Files,
		}
	}
	return HistoryEntry{Kind: e.Kind, By: e.By, At: s.timestamp(e.At), Files: s.files(e.Files)}
}

func rubricOut(r *schoolbox.Rubric) Rubric {
	out := Rubric{Title: r.Title, Mark: r.Mark, Levels: []string{}, Criteria: []Criterion{}}
	for _, l := range r.Levels {
		out.Levels = append(out.Levels, htmlToText(l))
	}
	for _, c := range r.Criteria {
		crit := Criterion{Name: htmlToText(c.Name), Mark: c.Mark, Indicators: []Indicator{}}
		for _, ind := range c.Indicators {
			row := Indicator{Name: htmlToText(ind.Name), Descriptors: []Descriptor{}}
			for _, d := range ind.Descriptors {
				row.Descriptors = append(row.Descriptors, Descriptor{
					Level: htmlToText(d.Level), Text: htmlToText(d.Text), Points: d.Points, Selected: d.Selected,
				})
			}
			crit.Indicators = append(crit.Indicators, row)
		}
		out.Criteria = append(out.Criteria, crit)
	}
	return out
}

func (s *service) files(files []schoolbox.AssessmentFile) []FileRef {
	out := make([]FileRef, 0, len(files))
	for _, f := range files {
		out = append(out, FileRef{
			Name: f.Name, Kind: f.Kind, Size: f.Size, MIME: f.MIME,
			URL: s.link(f.Link), Retrievable: f.Retrievable(),
		})
	}
	return out
}

// timestamp formats t as RFC 3339 in school time, or "" when unset.
func (s *service) timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(s.now().Location()).Format(time.RFC3339)
}

// pageFiles lists every file on the page once, in page order.
func pageFiles(p *schoolbox.AssessmentPage) []schoolbox.AssessmentFile {
	var files []schoolbox.AssessmentFile
	seen := map[string]bool{}
	add := func(fs []schoolbox.AssessmentFile) {
		for _, f := range fs {
			if !seen[f.Link] {
				seen[f.Link] = true
				files = append(files, f)
			}
		}
	}
	add(p.TaskFiles)
	for i := range p.Grading {
		add(p.Grading[i].Files)
	}
	for i := range p.History {
		add(p.History[i].Files)
		if f := p.History[i].Feedback; f != nil {
			add(f.Files)
		}
	}
	return files
}

// parseSize reads a displayed size such as "476.79 KB" as approximate bytes.
func parseSize(s string) *int64 {
	num, unit, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return nil
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return nil
	}
	mult := map[string]float64{"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30}[unit]
	if mult == 0 {
		return nil
	}
	return new(int64(math.Round(n * mult)))
}

type assessmentFileArgs struct {
	StudentID    int    `json:"student_id,omitempty" jsonschema:"student id from list_students; may be omitted when the parent has one child"`
	AssessmentID int    `json:"assessment_id" jsonschema:"assessment id, as passed to get_assessment"`
	Name         string `json:"name" jsonschema:"file name exactly as get_assessment lists it"`
}

// getAssessmentFile only fetches files the assessment page lists, so the tool
// cannot be pointed at arbitrary links.
func (s *service) getAssessmentFile(ctx context.Context, _ *mcp.CallToolRequest, a assessmentFileArgs) (*mcp.CallToolResult, any, error) {
	_, page, err := s.assessment(ctx, assessmentArgs{StudentID: a.StudentID, AssessmentID: a.AssessmentID})
	if err != nil {
		return nil, nil, err
	}
	files := pageFiles(&page)
	var file *schoolbox.AssessmentFile
	names := make([]string, 0, len(files))
	for i := range files {
		if !files[i].Retrievable() {
			continue
		}
		names = append(names, fmt.Sprintf("%q", files[i].Name))
		if files[i].Name == a.Name && file == nil {
			file = &files[i]
		}
	}
	if file == nil {
		for _, f := range files {
			if f.Name == a.Name {
				return nil, nil, fmt.Errorf("%q is a Kami annotation, which opens only in Schoolbox: %s", f.Name, s.link(f.Link))
			}
		}
		if len(names) == 0 {
			return nil, nil, fmt.Errorf("assessment %d has no files", a.AssessmentID)
		}
		return nil, nil, fmt.Errorf("assessment %d has no file %q; choose one of: %s", a.AssessmentID, a.Name, strings.Join(names, ", "))
	}
	body, contentType, err := s.client.Download(ctx, file.Link, maxAttachmentBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("download %q: %w", file.Name, err)
	}
	source := fmt.Sprintf("assessment %d", a.AssessmentID)
	return &mcp.CallToolResult{Content: fileContent(file.Name, file.MIME, contentType, s.link(file.Link), source, body)}, nil, nil
}
