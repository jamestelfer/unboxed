package schoolbox

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scripted answers each request with the next response from its queue, and
// records the requests.
type scripted struct {
	responses []*http.Response
	requests  []*http.Request
}

func (s *scripted) Do(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody}, nil
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func respond(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func login(session string) *http.Response {
	return respond(http.StatusOK, http.Header{"Set-Cookie": {"PHPSESSID=" + session + "; Path=/; HttpOnly"}}, `{"id":1}`)
}

func pdf(body string) *http.Response {
	return respond(http.StatusOK, http.Header{"Content-Type": {"application/pdf"}}, body)
}

func toLogin() *http.Response {
	return respond(http.StatusFound, http.Header{"Location": {"/login/"}}, "")
}

func newScriptedClient(t *testing.T, responses ...*http.Response) (*Client, *scripted) {
	t.Helper()
	doer := &scripted{responses: responses}
	c, err := New("https://school.example", "test-token", WithHTTPClient(doer))
	require.NoError(t, err)
	return c, doer
}

func TestDownloadLogsInOnceAndSendsSessionCookie(t *testing.T) {
	c, doer := newScriptedClient(t, login("s1"), pdf("%PDF-1"), pdf("%PDF-2"))

	body, mime, err := c.Download(t.Context(), "/storage/fetch.php?hash=a", 1<<20)
	require.NoError(t, err)
	assert.Equal(t, "%PDF-1", string(body))
	assert.Equal(t, "application/pdf", mime)

	_, _, err = c.Download(t.Context(), "/storage/fetch.php?hash=b", 1<<20)
	require.NoError(t, err)

	require.Len(t, doer.requests, 3)
	loginReq := doer.requests[0]
	assert.Equal(t, http.MethodPost, loginReq.Method)
	assert.Equal(t, "/api/session", loginReq.URL.Path)
	assert.Equal(t, "application/x-www-form-urlencoded", loginReq.Header.Get("Content-Type"))
	require.NoError(t, loginReq.ParseForm())
	assert.Equal(t, "test-token", loginReq.PostForm.Get("jwt"))
	assert.Empty(t, loginReq.URL.RawQuery, "token must not travel in the URL")

	for _, req := range doer.requests[1:] {
		ck, err := req.Cookie("PHPSESSID")
		require.NoError(t, err)
		assert.Equal(t, "s1", ck.Value)
	}
	assert.Equal(t, "hash=b", doer.requests[2].URL.RawQuery)
}

func TestDownloadLogsInAgainWhenSessionLapses(t *testing.T) {
	c, doer := newScriptedClient(t, login("s1"), pdf("one"), toLogin(), login("s2"), pdf("two"))

	_, _, err := c.Download(t.Context(), "/storage/fetch.php?hash=a", 1<<20)
	require.NoError(t, err)
	body, _, err := c.Download(t.Context(), "/storage/fetch.php?hash=b", 1<<20)
	require.NoError(t, err)

	assert.Equal(t, "two", string(body))
	require.Len(t, doer.requests, 5)
	ck, err := doer.requests[4].Cookie("PHPSESSID")
	require.NoError(t, err)
	assert.Equal(t, "s2", ck.Value)
}

func TestDownloadGivesUpAfterOneRelogin(t *testing.T) {
	c, doer := newScriptedClient(t, login("s1"), toLogin(), login("s2"), toLogin())

	_, _, err := c.Download(t.Context(), "/storage/fetch.php?hash=a", 1<<20)

	require.ErrorIs(t, err, ErrLoginRequired)
	assert.Len(t, doer.requests, 4)
}

func TestDownloadEnforcesLimit(t *testing.T) {
	c, _ := newScriptedClient(t, login("s1"), pdf("0123456789"))

	_, _, err := c.Download(t.Context(), "/storage/fetch.php?hash=a", 9)

	require.ErrorIs(t, err, ErrTooLarge)
}

func TestDownloadRejectsLinksOffSite(t *testing.T) {
	for _, link := range []string{"https://evil.example/x", "//evil.example/x", "storage/fetch.php"} {
		c, doer := newScriptedClient(t)

		_, _, err := c.Download(t.Context(), link, 1<<20)

		require.Error(t, err, link)
		assert.Contains(t, err.Error(), "site-relative")
		assert.Empty(t, doer.requests, "no request for %q", link)
	}
}

func TestDownloadReportsFailedLogin(t *testing.T) {
	c, _ := newScriptedClient(t, respond(http.StatusUnauthorized, nil, `{"message":"bad token"}`))

	_, _, err := c.Download(t.Context(), "/storage/fetch.php?hash=a", 1<<20)

	require.ErrorIs(t, err, ErrPermission)
}

func TestArticleDecodesAttachedFiles(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{"/news/29655": ok("news_article.json")})

	a, err := c.Article(t.Context(), 29655)

	require.NoError(t, err)
	require.Len(t, a.AttachedFiles, 1)
	f := a.AttachedFiles[0]
	assert.Equal(t, "Exam timetable.pdf", f.Name)
	assert.Equal(t, "application/pdf", f.Mime)
	assert.Equal(t, int64(2048), f.Size)
	assert.Equal(t, "/storage/fetch.php?hash=12ab34cd", f.Links.Fetch)
}
