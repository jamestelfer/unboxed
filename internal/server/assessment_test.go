package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	v := m.Run()
	dirty, err := snaps.Clean(m, snaps.CleanOpts{Sort: true})
	if err != nil {
		fmt.Println("clean snapshots:", err)
		os.Exit(1)
	}
	if dirty {
		fmt.Println("obsolete snapshots; run `just snapshots` to remove them")
		os.Exit(1)
	}
	os.Exit(v)
}

const (
	resubmissionPath = "/learning/assessments/9102/201"
	taskFileKey      = "/storage/fetch.php?hash=f85f28b4ce237162b78d19fc94cfe9e5abc37e7a&name=Hike%20Student%20Planning%20Assignment.docx"
	submissionKey    = "/learning/assessments/9102/201/responses/7002/files/7102/download"
)

func callAssessment(t *testing.T, s *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_assessment", Arguments: args})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)
	return res
}

func TestGetAssessment(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		student int
		id      int
	}{
		{"assessment_full.html", 201, 9101},
		{"assessment_upcoming.html", 202, 9107},
		{"assessment_upcoming_file.html", 202, 9108},
		{"assessment_percent.html", 201, 9106},
		{"assessment_grade.html", 202, 9105},
		{"assessment_not_submitted.html", 202, 9104},
		{"assessment_submitted.html", 202, 9103},
		{"assessment_resubmission.html", 201, 9102},
		{"assessment_rubric_marked.html", 202, 9110},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			s, _ := connect(t, map[string]string{fmt.Sprintf("/learning/assessments/%d/%d", tc.id, tc.student): tc.fixture})

			res := callAssessment(t, s, map[string]any{"student_id": tc.student, "assessment_id": tc.id})

			require.False(t, res.IsError, "%+v", res.Content)
			text, ok := res.Content[0].(*mcp.TextContent)
			require.True(t, ok, "content is %T", res.Content[0])
			snaps.MatchJSON(t, text.Text)
		})
	}
}

func TestGetAssessmentMarks(t *testing.T) {
	for fixture, want := range map[string]Feedback{
		"assessment_percent.html":       {Mark: "74 %", Teacher: "Mr Jordan South"},
		"assessment_grade.html":         {Mark: "Above The Expected Standard (B)"},
		"assessment_not_submitted.html": {Mark: "Not Submitted"},
		"assessment_resubmission.html":  {Action: "Resubmission required"},
	} {
		t.Run(fixture, func(t *testing.T) {
			s, _ := connect(t, map[string]string{"/learning/assessments/9100/201": fixture})

			var out assessmentOut
			mustCall(t, s, "get_assessment", map[string]any{"student_id": 201, "assessment_id": 9100}, &out)

			require.NotEmpty(t, out.Grading)
			got := out.Grading[0]
			assert.Equal(t, want.Mark, got.Mark)
			switch fixture {
			case "assessment_percent.html":
				assert.Equal(t, new(74.0), got.Percent)
				assert.Equal(t, "2026-11-03T19:50:00+11:00", got.At)
			case "assessment_grade.html":
				assert.Equal(t, "B", got.Grade)
			case "assessment_not_submitted.html":
				assert.Equal(t, "Not Submitted", got.Status)
				assert.Nil(t, got.Percent)
			case "assessment_resubmission.html":
				assert.Equal(t, want.Action, got.Action)
				assert.Empty(t, got.Mark)
				assert.Empty(t, got.Status)
			}
		})
	}
}

func TestGetAssessmentReturnsResourceLinks(t *testing.T) {
	s, _ := connect(t, map[string]string{resubmissionPath: "assessment_resubmission.html"})

	res := callAssessment(t, s, map[string]any{"student_id": 201, "assessment_id": 9102})

	require.False(t, res.IsError)
	links := make([]*mcp.ResourceLink, 0, len(res.Content)-1)
	for _, c := range res.Content[1:] {
		link, ok := c.(*mcp.ResourceLink)
		require.True(t, ok, "content is %T", c)
		links = append(links, link)
	}
	require.Len(t, links, 2, "task file and submission; the Kami link is not a file")
	assert.Equal(t, "https://school.example"+taskFileKey, links[0].URI)
	assert.Equal(t, "Hike Student Planning Assignment.docx", links[0].Name)
	assert.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", links[0].MIMEType)
	require.NotNil(t, links[0].Size)
	assert.Equal(t, int64(488233), *links[0].Size)
	assert.Equal(t, "https://school.example"+submissionKey, links[1].URI)
	assert.Nil(t, links[1].Size)
}

func TestGetAssessmentRequiresLinkedStudent(t *testing.T) {
	s, fx := connect(t, nil)

	msg := call(t, s, "get_assessment", map[string]any{"student_id": 999, "assessment_id": 9101}, nil)

	assert.Contains(t, msg, "201 (Alex Parent), 202 (Sam Parent)")
	assert.Equal(t, "/api/user/101", fx.lastRequest(), "no assessment request")
}

func TestGetAssessmentFailures(t *testing.T) {
	respond := func(status int, header http.Header, fixture string) func() *http.Response {
		return func() *http.Response {
			body := []byte{}
			if fixture != "" {
				var err error
				body, err = os.ReadFile("../schoolbox/testdata/" + fixture)
				if err != nil {
					panic(err)
				}
			}
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
		}
	}
	for name, tc := range map[string]struct {
		response func() *http.Response
		want     string
	}{
		"not visible":    {respond(http.StatusUnauthorized, nil, "assessment_denied.html"), "assessment 9101 is not visible for student 202 (Sam Parent)"},
		"not found":      {respond(http.StatusNotFound, nil, "assessment_not_found.html"), "no assessment 9101 for student 202 (Sam Parent)"},
		"login redirect": {respond(http.StatusFound, http.Header{"Location": {"/login?page=x"}}, ""), "rejected the token; it may have expired"},
		"login page":     {respond(http.StatusOK, nil, "assessment_login.html"), "rejected the token; it may have expired"},
		"layout drift":   {respond(http.StatusOK, nil, "home.html"), "unexpected page layout"},
	} {
		t.Run(name, func(t *testing.T) {
			s, fx := connect(t, nil)
			fx.raw = map[string]func() *http.Response{"/learning/assessments/9101/202": tc.response}

			msg := call(t, s, "get_assessment", map[string]any{"student_id": 202, "assessment_id": 9101}, nil)

			assert.Contains(t, msg, tc.want)
		})
	}
}

func TestGetAssessmentUndecodableRubricWarns(t *testing.T) {
	s, _ := connect(t, map[string]string{"/learning/assessments/9106/201": "assessment_rubric_broken.html"})

	var out assessmentOut
	mustCall(t, s, "get_assessment", map[string]any{"student_id": 201, "assessment_id": 9106}, &out)

	assert.Empty(t, out.Rubric)
	assert.Equal(t, []string{"rubric could not be read"}, out.Warnings)
	assert.NotEmpty(t, out.Grading)
}

func TestDueWorkAssessmentIDRoundTrips(t *testing.T) {
	start := time.Date(2026, 11, 23, 0, 0, 0, 0, melbourne)
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, melbourne)
	s, _ := connect(t, map[string]string{
		fmt.Sprintf("/calendar/ajax/full?end=%d&start=%d&userId=201", end.Unix(), start.Unix()): "calendar.json",
		"/learning/assessments/9182/201": "assessment_full.html",
	})
	var due calendarOut
	mustCall(t, s, "get_due_work", map[string]any{"student_id": 201, "from": "2026-11-23", "to": "2026-11-29"}, &due)
	require.Len(t, due.Events, 1)
	require.NotNil(t, due.Events[0].AssessmentID)

	var out assessmentOut
	mustCall(t, s, "get_assessment", map[string]any{"student_id": 201, "assessment_id": *due.Events[0].AssessmentID}, &out)

	assert.Equal(t, 9182, out.AssessmentID)
	assert.Equal(t, "https://school.example/learning/assessments/9182/201", out.Link)
}

func withAssessmentFiles(t *testing.T) (*mcp.ClientSession, *fixtures) {
	t.Helper()
	// The Kami annotation is renamed so it does not share the submission's name.
	s, fx := connect(t, map[string]string{resubmissionPath: "assessment_kami.html"})
	file := func(contentType, body string) func() *http.Response {
		return func() *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {contentType}},
				Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			}
		}
	}
	fx.raw = map[string]func() *http.Response{
		"/api/session": func() *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Set-Cookie": {"PHPSESSID=s1; Path=/"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":101}`))),
			}
		},
		taskFileKey:   file("application/octet-stream", "PK-task"),
		submissionKey: file("application/vnd.openxmlformats-officedocument.wordprocessingml.document", "PK-submission"),
	}
	return s, fx
}

func TestGetAssessmentFileDownloadsListedFiles(t *testing.T) {
	for name, want := range map[string]struct {
		uri, body string
	}{
		"Hike Student Planning Assignment.docx": {"https://school.example" + taskFileKey, "PK-task"},
		"hike_plan_alex":                        {"https://school.example" + submissionKey, "PK-submission"},
	} {
		t.Run(name, func(t *testing.T) {
			s, fx := withAssessmentFiles(t)

			res, err := s.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "get_assessment_file",
				Arguments: map[string]any{"student_id": 201, "assessment_id": 9102, "name": name},
			})

			require.NoError(t, err)
			require.False(t, res.IsError, "%+v", res.Content)
			require.Len(t, res.Content, 2)
			summary, ok := res.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			assert.Contains(t, summary.Text, "from assessment 9102")
			embedded, ok := res.Content[1].(*mcp.EmbeddedResource)
			require.True(t, ok, "content is %T", res.Content[1])
			assert.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", embedded.Resource.MIMEType)
			assert.Equal(t, want.uri, embedded.Resource.URI)
			assert.Equal(t, []byte(want.body), embedded.Resource.Blob)
			assert.Contains(t, fx.requests, "/api/session")
		})
	}
}

func TestGetAssessmentFileOnlyFetchesListedFiles(t *testing.T) {
	s, fx := withAssessmentFiles(t)

	msg := call(t, s, "get_assessment_file", map[string]any{"student_id": 201, "assessment_id": 9102, "name": "secret.docx"}, nil)

	assert.Contains(t, msg, `choose one of: "Hike Student Planning Assignment.docx", "hike_plan_alex"`)
	assert.Equal(t, resubmissionPath, fx.lastRequest(), "no download for an unlisted name")
}

func TestGetAssessmentFileRefusesKami(t *testing.T) {
	// The Kami annotation is renamed so it does not share the submission's name.
	s, fx := connect(t, map[string]string{resubmissionPath: "assessment_kami.html"})

	msg := call(t, s, "get_assessment_file", map[string]any{"student_id": 201, "assessment_id": 9102, "name": "Annotated plan"}, nil)

	assert.Contains(t, msg, `"Annotated plan" is a Kami annotation, which opens only in Schoolbox: https://school.example/learning/kami/7201`)
	assert.Equal(t, resubmissionPath, fx.lastRequest())
}
