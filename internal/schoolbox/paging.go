package schoolbox

import (
	"context"
	"iter"
	"maps"
	"net/url"
)

type page[T any] struct {
	Data     []T `json:"data"`
	Metadata struct {
		Cursor struct {
			Next *string `json:"next"`
		} `json:"cursor"`
	} `json:"metadata"`
}

// paginate follows metadata.cursor.next until it is null. A failed page yields
// one (zero, err) pair and ends the sequence.
func paginate[T any](ctx context.Context, c *Client, path string, query url.Values) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		q := url.Values{}
		maps.Copy(q, query)
		for {
			p, err := get[page[T]](ctx, c, path, q)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range p.Data {
				if !yield(item, nil) {
					return
				}
			}
			next := p.Metadata.Cursor.Next
			if next == nil || *next == "" || len(p.Data) == 0 {
				return
			}
			q.Set("cursor", *next)
		}
	}
}
