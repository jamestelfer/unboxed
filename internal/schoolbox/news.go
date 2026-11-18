package schoolbox

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Topic struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Sequence int    `json:"sequence"`
}

// Slug derives the topic's machine name, which /news/topics does not report:
// lower case, with each run of other characters replaced by a hyphen. It
// matches every topic on 26.1.16, e.g. "Camps & Excursions" is "camps-excursions".
func (t Topic) Slug() string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(t.Name) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// ArticleTopic is a topic as an article carries it, with its slug.
type ArticleTopic struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type ArticleSummary struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Blurb  string `json:"blurb"`
	Sticky bool   `json:"sticky"`
	Author struct {
		FullName string `json:"fullname"`
	} `json:"author"`
	Status      string         `json:"status"`
	PublishAt   time.Time      `json:"publishAt"`
	Attachments int            `json:"attachments"`
	ViewedAt    *time.Time     `json:"viewedAt"`
	Topics      []ArticleTopic `json:"topics"`
}

type Article struct {
	ID            int       `json:"id"`
	Title         string    `json:"title"`
	Blurb         string    `json:"blurb"`
	Body          string    `json:"body"`
	Sticky        bool      `json:"sticky"`
	Author        Person    `json:"author"`
	Status        string    `json:"status"`
	PublishAt     time.Time `json:"publishAt"`
	AttachedFiles []File    `json:"attachedFiles"`
}

// News lists articles aimed at the current user. The feed accepts a topic
// parameter but, on 26.1.16, only validates it and still returns every
// article, so callers filter on ArticleSummary.Topics instead.
func (c *Client) News(ctx context.Context) ([]ArticleSummary, error) {
	return get[[]ArticleSummary](ctx, c, "/news/lists/feed", nil)
}

func (c *Client) Article(ctx context.Context, id int) (Article, error) {
	resp, err := get[struct {
		Article Article `json:"article"`
	}](ctx, c, "/news/"+strconv.Itoa(id), nil)
	return resp.Article, err
}

func (c *Client) NewsTopics(ctx context.Context) ([]Topic, error) {
	return get[[]Topic](ctx, c, "/news/topics", nil)
}
