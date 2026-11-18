package schoolbox

import (
	"context"
	json "encoding/json/v2"
	"iter"
	"net/url"
	"time"
)

// SearchTypes are the result types /api/search can be filtered to. A parent
// token sees only results the parent could open in the browser.
var SearchTypes = []string{
	"assessment", "assessment.dueWork", "assessment.lessonPlan", "assessment.project", "assessment.task", "assessment.quiz",
	"badge", "blog", "event",
	"file", "file.audio", "file.document", "file.image", "file.video", "file.other",
	"forum", "link", "news",
	"page", "page.class", "page.course", "page.unit", "page.homepage", "page.other",
	"textBox", "tile",
	"user", "user.guest", "user.parent", "user.staff", "user.student",
}

// SearchFilter narrows a search. From and To are YYYY-MM-DD; what the date
// range means depends on the result type.
type SearchFilter struct {
	Types []string
	From  string
	To    string
}

func (f SearchFilter) encode() (string, error) {
	type span struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	}
	var wire struct {
		Types []string `json:"types,omitempty"`
		Date  *span    `json:"date,omitempty"`
	}
	wire.Types = f.Types
	if f.From != "" || f.To != "" {
		wire.Date = &span{From: f.From, To: f.To}
	}
	if wire.Types == nil && wire.Date == nil {
		return "", nil
	}
	b, err := json.Marshal(wire)
	return string(b), err
}

type SearchResult struct {
	// A string, unlike the integer ids elsewhere.
	ID   string    `json:"id"`
	Type string    `json:"type"`
	Date time.Time `json:"date"`
	// Site-relative; empty for some events.
	URL   string `json:"url"`
	Title string `json:"title"`
	// A one-line byline, e.g. "Posted 6 days ago by Ms Terry Teacher"; often null.
	Meta string `json:"meta"`
	// A plain-text excerpt around the match; may contain HTML.
	Description string `json:"description"`
	// Null for results without an author.
	Creator struct {
		FullName string `json:"fullName"`
	} `json:"creator"`
	// The folders the result sits in, outermost first.
	Context []SearchContext `json:"context"`
}

type SearchContext struct {
	Name string `json:"name"`
}

// Search pages through results for keyword, most relevant first, fetching
// pages as the caller ranges. Schoolbox requires at least two characters.
func (c *Client) Search(ctx context.Context, keyword string, filter SearchFilter) iter.Seq2[SearchResult, error] {
	return func(yield func(SearchResult, error) bool) {
		q := url.Values{}
		q.Set("keyword", keyword)
		f, err := filter.encode()
		if err != nil {
			yield(SearchResult{}, err)
			return
		}
		if f != "" {
			q.Set("filter", f)
		}
		paginate[SearchResult](ctx, c, "/api/search", q)(yield)
	}
}
