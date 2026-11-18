// Package grades turns Schoolbox due-work feedback into comparable scores and
// summary statistics.
package grades

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Score is the comparable form of a free-text mark. Percent is nil when the
// mark is not an achievement score, such as a completion status.
type Score struct {
	Percent *float64 `json:"percent,omitempty"`
	Grade   string   `json:"grade,omitempty"`
	// Status holds word-only marks such as "Submitted" or "Not Satisfactory".
	Status string `json:"status,omitempty"`
	// GradeBand is set when Percent is Schoolbox's stand-in for a descriptor
	// grade (C becomes 70, B 85) rather than a score the student earned.
	GradeBand bool `json:"gradeBand,omitzero"`
}

var (
	fractionPattern = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?)\s*/\s*(\d+(?:\.\d+)?)\s*$`)
	percentPattern  = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?)\s*%\s*$`)
	// Trailing bracketed grade, as in "Above The Expected Standard (B)".
	bracketGradePattern = regexp.MustCompile(`\(([A-E][+-]?)\)\s*$`)
	letterGradePattern  = regexp.MustCompile(`^\s*([A-E][+-]?)\s*$`)
	digitPattern        = regexp.MustCompile(`\d`)
)

// Parse reads a mark. Word-only marks ("Submitted", "Not Satisfactory") are
// statuses: Schoolbox normalises them to 0, 25 or 75, which would skew any
// average, so they get no percentage. Otherwise the normalised mark (a
// percentage) wins, then "x / y" and "x %" forms. The grade is a letter from
// the mark text, else from the gradient mark when that is a letter; this
// school's gradient marks are a 0-9 scale.
func Parse(mark string, normalised *float64, gradient *string) Score {
	var s Score
	mark = strings.TrimSpace(mark)
	if m := bracketGradePattern.FindStringSubmatch(mark); m != nil {
		s.Grade = m[1]
	} else if m := letterGradePattern.FindStringSubmatch(mark); m != nil {
		s.Grade = m[1]
	} else if gradient != nil && letterGradePattern.MatchString(*gradient) {
		s.Grade = strings.TrimSpace(*gradient)
	}
	if mark != "" && s.Grade == "" && !digitPattern.MatchString(mark) {
		s.Status = mark
		return s
	}
	switch {
	case normalised != nil:
		s.Percent = new(round(*normalised))
		s.GradeBand = s.Grade != "" && !digitPattern.MatchString(mark)
	case fractionPattern.MatchString(mark):
		m := fractionPattern.FindStringSubmatch(mark)
		num, _ := strconv.ParseFloat(m[1], 64)
		den, _ := strconv.ParseFloat(m[2], 64)
		if den > 0 {
			s.Percent = new(round(num / den * 100))
		}
	case percentPattern.MatchString(mark):
		v, _ := strconv.ParseFloat(percentPattern.FindStringSubmatch(mark)[1], 64)
		s.Percent = new(v)
	}
	return s
}

// Result is one marked piece of work, the input to Summarise.
type Result struct {
	Title    string
	Subject  string
	WorkType string
	Weight   float64
	Marked   time.Time
	Score    Score
}

type Distribution struct {
	Count  int     `json:"count"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	StdDev float64 `json:"stdDev"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	P25    float64 `json:"p25"`
	P75    float64 `json:"p75"`
	// Mean weighted by assessment weight; nil when no item carries a weight.
	WeightedMean *float64 `json:"weightedMean,omitempty"`
}

type Group struct {
	Name    string         `json:"name"`
	Items   int            `json:"items"`
	Percent *Distribution  `json:"percent,omitempty"`
	Grades  map[string]int `json:"grades,omitempty"`
}

type Trend struct {
	// Least-squares slope of percent against marking date.
	PercentPer30Days float64 `json:"percentPer30Days"`
	FirstHalfMean    float64 `json:"firstHalfMean"`
	SecondHalfMean   float64 `json:"secondHalfMean"`
}

type Highlight struct {
	Title   string  `json:"title"`
	Subject string  `json:"subject"`
	Percent float64 `json:"percent"`
	Marked  string  `json:"marked"`
}

type Summary struct {
	Items    int           `json:"items"`
	Unscored int           `json:"unscored"`
	Percent  *Distribution `json:"percent,omitempty"`
	// Percent restricted to numeric marks, excluding grade-band stand-ins.
	MarkPercent    *Distribution  `json:"markPercent,omitempty"`
	GradeBandItems int            `json:"gradeBandItems"`
	Grades         map[string]int `json:"grades,omitempty"`
	// Completion marks such as "Submitted" or "Not Satisfactory", by count.
	Statuses   map[string]int `json:"statuses,omitempty"`
	BySubject  []Group        `json:"bySubject"`
	ByWorkType []Group        `json:"byWorkType"`
	// Needs at least 4 scored items spread over more than one day.
	Trend  *Trend      `json:"trend,omitempty"`
	Best   []Highlight `json:"best,omitempty"`
	Lowest []Highlight `json:"lowest,omitempty"`
}

// Summarise computes statistics over results. Results without a percentage
// count towards grade tallies and Unscored but not the distribution.
func Summarise(results []Result) Summary {
	s := Summary{
		Items:      len(results),
		BySubject:  groupBy(results, func(r Result) string { return r.Subject }),
		ByWorkType: groupBy(results, func(r Result) string { return r.WorkType }),
	}
	s.Percent, s.Grades = describe(results)
	var numeric []Result
	for i := range results {
		r := &results[i]
		if r.Score.GradeBand {
			s.GradeBandItems++
		} else {
			numeric = append(numeric, *r)
		}
	}
	s.MarkPercent, _ = describe(numeric)
	var scored []Result
	for i := range results {
		r := &results[i]
		if st := r.Score.Status; st != "" {
			if s.Statuses == nil {
				s.Statuses = map[string]int{}
			}
			s.Statuses[st]++
		}
		if r.Score.Percent == nil {
			s.Unscored++
		} else {
			scored = append(scored, *r)
		}
	}
	s.Trend = trend(scored)
	slices.SortStableFunc(scored, func(a, b Result) int {
		return cmp.Compare(*b.Score.Percent, *a.Score.Percent)
	})
	n := min(3, len(scored))
	for i := range scored[:n] {
		r := &scored[:n][i]
		s.Best = append(s.Best, highlight(*r))
	}
	for i := len(scored) - 1; i >= len(scored)-n; i-- {
		s.Lowest = append(s.Lowest, highlight(scored[i]))
	}
	return s
}

func describe(results []Result) (*Distribution, map[string]int) {
	var pcts []float64
	var wsum, wtotal float64
	var grades map[string]int
	for i := range results {
		r := &results[i]
		if g := r.Score.Grade; g != "" {
			if grades == nil {
				grades = map[string]int{}
			}
			grades[g]++
		}
		if r.Score.Percent == nil {
			continue
		}
		p := *r.Score.Percent
		pcts = append(pcts, p)
		if r.Weight > 0 {
			wsum += p * r.Weight
			wtotal += r.Weight
		}
	}
	if len(pcts) == 0 {
		return nil, grades
	}
	slices.Sort(pcts)
	var sum float64
	for _, p := range pcts {
		sum += p
	}
	mean := sum / float64(len(pcts))
	var sq float64
	for _, p := range pcts {
		sq += (p - mean) * (p - mean)
	}
	d := &Distribution{
		Count:  len(pcts),
		Mean:   round(mean),
		Median: round(quantile(pcts, 0.5)),
		Min:    pcts[0],
		Max:    pcts[len(pcts)-1],
		P25:    round(quantile(pcts, 0.25)),
		P75:    round(quantile(pcts, 0.75)),
	}
	if len(pcts) > 1 {
		// Sample standard deviation.
		d.StdDev = round(math.Sqrt(sq / float64(len(pcts)-1)))
	}
	if wtotal > 0 {
		d.WeightedMean = new(round(wsum / wtotal))
	}
	return d, grades
}

// quantile interpolates linearly between closest ranks of sorted values.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func groupBy(results []Result, key func(Result) string) []Group {
	byName := map[string][]Result{}
	for i := range results {
		r := &results[i]
		k := key(*r)
		if k == "" {
			k = "(none)"
		}
		byName[k] = append(byName[k], *r)
	}
	groups := make([]Group, 0, len(byName))
	for name, rs := range byName {
		pct, grades := describe(rs)
		groups = append(groups, Group{Name: name, Items: len(rs), Percent: pct, Grades: grades})
	}
	slices.SortFunc(groups, func(a, b Group) int { return strings.Compare(a.Name, b.Name) })
	return groups
}

func trend(scored []Result) *Trend {
	if len(scored) < 4 {
		return nil
	}
	sorted := slices.Clone(scored)
	slices.SortStableFunc(sorted, func(a, b Result) int { return a.Marked.Compare(b.Marked) })
	origin := sorted[0].Marked
	var sx, sy, sxx, sxy float64
	n := float64(len(sorted))
	for i := range sorted {
		r := &sorted[i]
		x := r.Marked.Sub(origin).Hours() / 24
		y := *r.Score.Percent
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return nil
	}
	half := len(sorted) / 2
	return &Trend{
		PercentPer30Days: round((n*sxy - sx*sy) / den * 30),
		FirstHalfMean:    round(meanPercent(sorted[:half])),
		SecondHalfMean:   round(meanPercent(sorted[half:])),
	}
}

func meanPercent(rs []Result) float64 {
	var sum float64
	for i := range rs {
		r := &rs[i]
		sum += *r.Score.Percent
	}
	return sum / float64(len(rs))
}

func highlight(r Result) Highlight {
	return Highlight{Title: r.Title, Subject: r.Subject, Percent: *r.Score.Percent, Marked: r.Marked.Format(time.DateOnly)}
}

func round(v float64) float64 { return math.Round(v*10) / 10 }
