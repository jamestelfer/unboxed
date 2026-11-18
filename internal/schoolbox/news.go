package schoolbox

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

type Topic struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Sequence int    `json:"sequence"`
}

type ArticleSummary struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Blurb  string `json:"blurb"`
	Sticky bool   `json:"sticky"`
	Author struct {
		FullName string `json:"fullname"`
	} `json:"author"`
	Status      string     `json:"status"`
	PublishAt   time.Time  `json:"publishAt"`
	Attachments int        `json:"attachments"`
	ViewedAt    *time.Time `json:"viewedAt"`
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

// News lists articles aimed at the current user, optionally limited to a topic slug.
func (c *Client) News(ctx context.Context, topic string) ([]ArticleSummary, error) {
	q := url.Values{}
	if topic != "" {
		q.Set("topic", topic)
	}
	return get[[]ArticleSummary](ctx, c, "/news/lists/feed", q)
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
