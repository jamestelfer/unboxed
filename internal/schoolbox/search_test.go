package schoolbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const newsFilter = "filter=%7B%22types%22%3A%5B%22news%22%5D%2C%22date%22%3A%7B%22from%22%3A%222026-01-01%22%7D%7D"

func TestSearchSendsFilterAndFollowsCursor(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{
		"/api/search?" + newsFilter + "&keyword=camp":                       ok("search_page1.json"),
		"/api/search?cursor=Y3Vyc29yLTI%3D&" + newsFilter + "&keyword=camp": ok("search_page2.json"),
	})

	var got []SearchResult
	for r, err := range c.Search(t.Context(), "camp", SearchFilter{Types: []string{"news"}, From: "2026-01-01"}) {
		require.NoError(t, err)
		got = append(got, r)
	}

	require.Len(t, got, 3)
	news, page, event := got[0], got[1], got[2]
	assert.Equal(t, "29648", news.ID)
	assert.Equal(t, "news", news.Type)
	assert.Equal(t, "/news/29648?ref=search", news.URL)
	assert.True(t, news.Date.Equal(time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)), "date %v", news.Date)
	assert.Equal(t, "Ms Terry Teacher", news.Creator.FullName)
	assert.Empty(t, page.Meta, "null meta")
	assert.Equal(t, []SearchContext{{Name: "Year 9"}, {Name: "Camps"}}, page.Context)
	assert.Empty(t, page.Creator.FullName, "null creator")
	assert.Equal(t, "Year 9 Camp", event.Title)
}

func TestSearchOmitsEmptyFilter(t *testing.T) {
	c, _ := newTestClient(t, map[string]route{"/api/search?keyword=camp+list": ok("search_page2.json")})

	for _, err := range c.Search(t.Context(), "camp list", SearchFilter{}) {
		require.NoError(t, err)
	}
}
