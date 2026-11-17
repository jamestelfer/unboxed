package schoolbox

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"iter"
	"strconv"
	"time"
)

type FeedKind string

const (
	KindDueWorkFeedback FeedKind = "dueWorkFeedback"
	// Taken from spec examples; not yet seen in a real response.
	KindLearningMoment FeedKind = "evidence"
	KindBadgeAward     FeedKind = "badgeAward"
)

// FeedItem is one entry in a user's evidence feed. DueWorkFeedback is set for
// that kind; Object always holds the raw payload, for kinds without a model.
type FeedItem struct {
	Kind            FeedKind
	Date            time.Time
	DueWorkFeedback *DueWorkFeedback
	Object          jsontext.Value
}

// UnmarshalJSONFrom buffers the object because the discriminator may follow it in the stream.
func (f *FeedItem) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Discriminator FeedKind       `json:"discriminator"`
		Date          time.Time      `json:"date"`
		Object        jsontext.Value `json:"object"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	*f = FeedItem{Kind: wire.Discriminator, Date: wire.Date, Object: wire.Object}
	if f.Kind == KindDueWorkFeedback {
		v, err := decodeAs[DueWorkFeedback](wire.Object)
		if err != nil {
			return err
		}
		f.DueWorkFeedback = v
	}
	return nil
}

func decodeAs[T any](b []byte) (*T, error) {
	v := new(T)
	if err := json.Unmarshal(b, v); err != nil {
		return nil, err
	}
	return v, nil
}

type DueWorkFeedback struct {
	ID int `json:"id"`
	// Free text in whatever form the teacher used: "53 %", "21.5 / 30",
	// "Above The Expected Standard (B)", or empty.
	Mark           string     `json:"mark"`
	NormalisedMark *float64   `json:"normalisedMark"`
	GradientMark   *string    `json:"gradientMark"`
	Comment        string     `json:"comment"`
	PublishedAt    time.Time  `json:"publishedAt"`
	Instructor     Person     `json:"instructor"`
	Assessment     Assessment `json:"assessment"`
	Links          struct {
		Summary string `json:"summary"`
	} `json:"_links"`
}

type Assessment struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	WorkType    string    `json:"workType"`
	FolderID    int       `json:"folderId"`
	FolderName  string    `json:"folderName"`
	Weight      float64   `json:"weight"`
	PublishedAt time.Time `json:"publishedAt"`
	OpenAt      time.Time `json:"openAt"`
	DueAt       time.Time `json:"dueAt"`
	CloseAt     time.Time `json:"closeAt"`
}

// Feed lists a user's evidence feed, newest first, fetching pages as the
// caller ranges. A parent may read their own and their children's feeds.
func (c *Client) Feed(ctx context.Context, userID int) iter.Seq2[FeedItem, error] {
	return paginate[FeedItem](ctx, c, "/learning/evidenceFeed/user/"+strconv.Itoa(userID), nil)
}
