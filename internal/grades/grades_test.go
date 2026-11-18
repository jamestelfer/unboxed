package grades

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	for name, tc := range map[string]struct {
		mark       string
		normalised *float64
		gradient   *string
		want       Score
	}{
		"normalised wins":     {"53 %", new(54.0), new("C"), Score{Percent: new(54.0), Grade: "C"}},
		"normalised rounded":  {"21.5 / 30", new(71.66666666667), new("7"), Score{Percent: new(71.7)}},
		"fraction":            {"21.5 / 30", nil, nil, Score{Percent: new(71.7)}},
		"percent":             {"88%", nil, nil, Score{Percent: new(88.0)}},
		"descriptive bracket": {"At The Expected Standard (C)", new(70.0), new("7"), Score{Percent: new(70.0), Grade: "C", GradeBand: true}},
		"bare letter":         {"A-", nil, nil, Score{Grade: "A-"}},
		"numeric gradient":    {"14 / 15", nil, new("9"), Score{Percent: new(93.3)}},
		"submitted":           {"Submitted", new(0.0), new("0"), Score{Status: "Submitted"}},
		"not satisfactory":    {"Not Satisfactory", new(25.0), new("2"), Score{Status: "Not Satisfactory"}},
		"empty":               {"", nil, nil, Score{}},
		"zero denominator":    {"3 / 0", nil, nil, Score{}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Parse(tc.mark, tc.normalised, tc.gradient))
		})
	}
}

func TestSummarise(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 7, d, 0, 0, 0, 0, time.UTC) }
	results := []Result{
		{Title: "a", Subject: "English", WorkType: "Task", Weight: 1, Marked: day(1), Score: Score{Percent: new(60.0), Grade: "C"}},
		{Title: "b", Subject: "English", WorkType: "Task", Weight: 3, Marked: day(11), Score: Score{Percent: new(70.0), Grade: "B"}},
		{Title: "c", Subject: "Maths", WorkType: "Test", Marked: day(21), Score: Score{Percent: new(80.0)}},
		{Title: "d", Subject: "Maths", WorkType: "Test", Marked: day(31), Score: Score{Percent: new(90.0), Grade: "A", GradeBand: true}},
		{Title: "e", Subject: "Art", Marked: day(15), Score: Score{Grade: "B"}},
		{Title: "f", Subject: "Art", Marked: day(16), Score: Score{Status: "Submitted"}},
	}

	got := Summarise(results)

	assert.Equal(t, 6, got.Items)
	assert.Equal(t, 2, got.Unscored)
	assert.Equal(t, map[string]int{"Submitted": 1}, got.Statuses)
	assert.Equal(t, map[string]int{"A": 1, "B": 2, "C": 1}, got.Grades)

	require.NotNil(t, got.Percent)
	assert.Equal(t, Distribution{
		Count: 4, Mean: 75, Median: 75, StdDev: 12.9, Min: 60, Max: 90, P25: 67.5, P75: 82.5,
		WeightedMean: new(67.5),
	}, *got.Percent)

	assert.Equal(t, 1, got.GradeBandItems)
	require.NotNil(t, got.MarkPercent)
	assert.Equal(t, 3, got.MarkPercent.Count)
	assert.InDelta(t, 70, got.MarkPercent.Mean, 0.01)

	require.Len(t, got.BySubject, 3)
	assert.Equal(t, "Art", got.BySubject[0].Name)
	assert.Nil(t, got.BySubject[0].Percent)
	require.NotNil(t, got.BySubject[2].Percent)
	assert.InDelta(t, 85, got.BySubject[2].Percent.Mean, 0.01)
	assert.Equal(t, "(none)", got.ByWorkType[0].Name)

	assert.Equal(t, &Trend{PercentPer30Days: 30, FirstHalfMean: 65, SecondHalfMean: 85}, got.Trend)

	require.Len(t, got.Best, 3)
	assert.Equal(t, "d", got.Best[0].Title)
	assert.Equal(t, "a", got.Lowest[0].Title)
}

func TestSummariseEmpty(t *testing.T) {
	got := Summarise(nil)

	assert.Nil(t, got.Percent)
	assert.Nil(t, got.Trend)
	assert.Nil(t, got.Best)
}
