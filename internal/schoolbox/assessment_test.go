package schoolbox

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gkampitakis/go-snaps/snaps"
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

var melbourne = func() *time.Location {
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		panic(err)
	}
	return loc
}()

// pageDoer answers every request with one status, header set and fixture.
type pageDoer struct {
	status  int
	header  http.Header
	fixture string
	paths   []string
}

func (d *pageDoer) Do(req *http.Request) (*http.Response, error) {
	d.paths = append(d.paths, req.URL.Path)
	body := []byte{}
	if d.fixture != "" {
		var err error
		if body, err = os.ReadFile(filepath.Join("testdata", d.fixture)); err != nil {
			return nil, err
		}
	}
	return &http.Response{StatusCode: d.status, Header: d.header, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func fetchAssessment(t *testing.T, d *pageDoer, assessmentID, studentID int) (AssessmentPage, error) {
	t.Helper()
	c, err := New("https://school.example", "test-token", WithHTTPClient(d))
	require.NoError(t, err)
	return c.Assessment(t.Context(), assessmentID, studentID, melbourne)
}

func TestAssessmentPages(t *testing.T) {
	for _, tc := range []struct {
		fixture                 string
		assessmentID, studentID int
	}{
		{"assessment_full.html", 9101, 201},
		{"assessment_upcoming.html", 9107, 202},
		{"assessment_upcoming_file.html", 9108, 202},
		{"assessment_percent.html", 9106, 201},
		{"assessment_grade.html", 9105, 202},
		{"assessment_not_submitted.html", 9104, 202},
		{"assessment_submitted.html", 9103, 202},
		{"assessment_online_submission.html", 9109, 202},
		{"assessment_resubmission.html", 9102, 201},
		{"assessment_rubric_marked.html", 9102, 201},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			d := &pageDoer{status: http.StatusOK, fixture: tc.fixture}

			page, err := fetchAssessment(t, d, tc.assessmentID, tc.studentID)

			require.NoError(t, err)
			assert.Equal(t, []string{AssessmentPath(tc.assessmentID, tc.studentID)}, d.paths)
			out, err := json.Marshal(page)
			require.NoError(t, err)
			snaps.MatchJSON(t, out)
		})
	}
}

func TestAssessmentFullPageIgnoresChrome(t *testing.T) {
	page, err := fetchAssessment(t, &pageDoer{status: http.StatusOK, fixture: "assessment_full.html"}, 9101, 201)

	require.NoError(t, err)
	assert.Equal(t, "Local Produce Research Task", page.Title)
	assert.Equal(t, time.Date(2026, 10, 2, 23, 59, 0, 0, melbourne).Unix(), page.Due.Unix())
	require.Len(t, page.Grading, 1)
	assert.Equal(t, "28 / 40", page.Grading[0].Mark)
	assert.Equal(t, time.Date(2026, 11, 4, 12, 36, 0, 0, melbourne), page.Grading[0].At)
}

func TestAssessmentRubricSelection(t *testing.T) {
	page, err := fetchAssessment(t, &pageDoer{status: http.StatusOK, fixture: "assessment_rubric_marked.html"}, 9102, 201)

	require.NoError(t, err)
	require.Len(t, page.Rubrics, 1)
	r := page.Rubrics[0]
	assert.Equal(t, "30 / 36", r.Mark)
	require.NotEmpty(t, r.Criteria)
	first := r.Criteria[0].Indicators[0].Descriptors
	require.Len(t, first, 5)
	assert.False(t, first[0].Selected)
	assert.True(t, first[1].Selected)
	assert.Equal(t, "<p>Above Standard</p>\n", first[1].Level)
}

func TestAssessmentSubmissionLinks(t *testing.T) {
	for fixture, want := range map[string]AssessmentFile{
		"assessment_submitted.html": {
			Name: "Week 1 & 2 - English Reflection",
			Kind: FileSubmission,
			MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Link: "/learning/assessments/9103/202/responses/7001/files/7101/download",
		},
		// Text typed into the online editor links to a preview page; its
		// content is at the same path with /download appended.
		"assessment_online_submission.html": {
			Name: "Online Submission",
			Kind: FileSubmission,
			MIME: "text/html",
			Link: "/learning/assessments/9109/202/responses/7001/files/7101/download",
		},
	} {
		t.Run(fixture, func(t *testing.T) {
			page, err := fetchAssessment(t, &pageDoer{status: http.StatusOK, fixture: fixture}, 9103, 202)

			require.NoError(t, err)
			var files []AssessmentFile
			for _, e := range page.History {
				if e.Kind == HistorySubmission {
					files = append(files, e.Files...)
				}
			}
			assert.Equal(t, []AssessmentFile{want}, files)
		})
	}
}

func TestAssessmentUndecodableRubricWarns(t *testing.T) {
	page, err := fetchAssessment(t, &pageDoer{status: http.StatusOK, fixture: "assessment_rubric_broken.html"}, 9106, 201)

	require.NoError(t, err)
	assert.Empty(t, page.Rubrics)
	assert.Equal(t, []string{"rubric could not be read"}, page.Warnings)
	assert.NotEmpty(t, page.Grading, "the rest of the page is still read")
}

func TestAssessmentFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		doer *pageDoer
		want error
	}{
		"not a linked student": {&pageDoer{status: http.StatusUnauthorized, fixture: "assessment_denied.html"}, ErrPermission},
		"unknown assessment":   {&pageDoer{status: http.StatusNotFound, fixture: "assessment_not_found.html"}, ErrNotFound},
		"redirect to login": {&pageDoer{
			status: http.StatusFound,
			header: http.Header{"Location": {"https://school.example/login?page=%2Flearning%2Fassessments%2F9101%2F201"}},
		}, ErrLoginRequired},
		"login page served as 200": {&pageDoer{status: http.StatusOK, fixture: "assessment_login.html"}, ErrLoginRequired},
		"page without anchors":     {&pageDoer{status: http.StatusOK, fixture: "home.html"}, ErrUnexpectedPage},
		"error page served as 200": {&pageDoer{status: http.StatusOK, fixture: "assessment_denied.html"}, ErrUnexpectedPage},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fetchAssessment(t, tc.doer, 9101, 201)

			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestOtherRedirectsAreAPIErrors(t *testing.T) {
	d := &pageDoer{status: http.StatusSeeOther, header: http.Header{"Location": {"/learning/assessments/9101/201"}}}

	_, err := fetchAssessment(t, d, 9101, 0)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLoginRequired)
	apiErr, ok := errors.AsType[*APIError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusSeeOther, apiErr.StatusCode)
}
