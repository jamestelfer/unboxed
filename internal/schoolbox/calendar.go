package schoolbox

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Event is one calendar entry: a timetabled class, a school or personal event,
// or due work. ClassName's source class and Meta.Type identify which.
type Event struct {
	Title  string
	Start  time.Time
	End    time.Time
	AllDay bool
	// Space-separated source/type/level classes, e.g. "source1 type3 campus1".
	ClassName string
	Meta      EventMeta
	// Site-relative link to the event or task page, when there is one.
	Path string
}

// Event sources, from the "sourceN" class. Observed on 26.1.16.
const (
	SourceSchool    = "source1"
	SourceDueWork   = "source2"
	SourceTimetable = "source5"
)

// Source returns the "sourceN" class, or "" when there is none.
func (e Event) Source() string {
	for f := range strings.FieldsSeq(e.ClassName) {
		if strings.HasPrefix(f, "source") {
			return f
		}
	}
	return ""
}

func (e Event) IsDueWork() bool {
	return e.Source() == SourceDueWork || e.Meta.Type == "Due Work"
}

type EventMeta struct {
	// Detail may contain HTML.
	Detail      string `json:"detail"`
	Location    string `json:"location"`
	EventTypeID int    `json:"eventTypeId"`
	// Friendly category label, e.g. "Meeting".
	Type      string `json:"type"`
	Variant   string `json:"variant"`
	Author    string `json:"author"`
	Completed bool   `json:"completed"`

	// Due work only. Title omits the class name that Event.Title carries.
	Title          string `json:"title"`
	FolderName     string `json:"folderName"`
	WorkType       string `json:"workType"`
	AssessmentType string `json:"assessmentType"`
}

func (e *Event) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Title     string `json:"title"`
		Start     string `json:"start"`
		End       string `json:"end"`
		AllDay    bool   `json:"allDay"`
		ClassName string `json:"className"`
		Data      struct {
			Meta EventMeta `json:"meta"`
			// An object for most events but [] for due work.
			Links  jsontext.Value `json:"links"`
			Custom struct {
				MoreDetailsLink any `json:"moreDetailsLink"`
			} `json:"custom"`
		} `json:"data"`
	}
	if err := json.UnmarshalDecode(dec, &wire); err != nil {
		return err
	}
	start, err := parseCalendarTime(wire.Start)
	if err != nil {
		return fmt.Errorf("event %q start: %w", wire.Title, err)
	}
	end, err := parseCalendarTime(wire.End)
	if err != nil {
		return fmt.Errorf("event %q end: %w", wire.Title, err)
	}
	*e = Event{
		Title:     wire.Title,
		Start:     start,
		End:       end,
		AllDay:    wire.AllDay,
		ClassName: wire.ClassName,
		Meta:      wire.Data.Meta,
	}
	if wire.Data.Links.Kind() == '{' {
		var links struct {
			Path *string `json:"path"`
		}
		if err := json.Unmarshal(wire.Data.Links, &links); err != nil {
			return fmt.Errorf("event %q links: %w", wire.Title, err)
		}
		if links.Path != nil {
			e.Path = *links.Path
		}
	}
	// moreDetailsLink is false when absent.
	if link, ok := wire.Data.Custom.MoreDetailsLink.(string); ok && e.Path == "" {
		e.Path = link
	}
	return nil
}

// parseCalendarTime accepts RFC 3339 or, for all-day events, a bare date in local time.
func parseCalendarTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if !strings.Contains(s, "T") {
		return time.ParseInLocation(time.DateOnly, s, time.Local)
	}
	return time.Parse(time.RFC3339, s)
}

// Calendar lists a user's events that overlap [start, end): timetable, school,
// group and personal events. Schoolbox recommends ranges of a month or less.
func (c *Client) Calendar(ctx context.Context, userID int, start, end time.Time) ([]Event, error) {
	q := url.Values{}
	q.Set("userId", strconv.Itoa(userID))
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	return get[[]Event](ctx, c, "/calendar/ajax/full", q)
}
