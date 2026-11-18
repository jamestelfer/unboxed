package schoolbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalendarSendsUnixRangeAndDecodesEvents(t *testing.T) {
	start := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	c, _ := newTestClient(t, map[string]route{
		"/calendar/ajax/full?end=1795996800&start=1795392000&userId=42": ok("calendar.json"),
	})

	events, err := c.Calendar(t.Context(), 42, start, start.AddDate(0, 0, 7))

	require.NoError(t, err)
	require.Len(t, events, 3)
	class, due, allDay := events[0], events[1], events[2]

	assert.Equal(t, "Year 9 English", class.Title)
	assert.Equal(t, "B12", class.Meta.Location)
	assert.Empty(t, class.Path)
	assert.True(t, class.Start.Equal(time.Date(2026, 11, 22, 22, 0, 0, 0, time.UTC)), "start %v", class.Start)
	assert.Equal(t, SourceTimetable, class.Source())
	assert.False(t, class.IsDueWork())

	assert.True(t, due.IsDueWork())
	assert.Equal(t, "3 - Persuasive essay", due.Meta.Title)
	assert.Equal(t, "Homework", due.Meta.WorkType)
	// Due work sends links as [], so the path comes from data.custom.
	assert.Equal(t, "/learning/assessments/9182/201?returnUrl=L2xlYXJuaW5n", due.Path)
	assert.True(t, due.End.IsZero())

	assert.True(t, allDay.AllDay)
	assert.Equal(t, "2026-11-27", allDay.Start.Format(time.DateOnly))
	assert.Equal(t, time.Local, allDay.Start.Location())
	assert.Equal(t, SourceSchool, allDay.Source())
}
