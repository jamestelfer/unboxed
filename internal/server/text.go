package server

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	blankLines  = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)
	spaceRuns   = regexp.MustCompile(`[ \t\x{a0}]+`)
	blockBreaks = map[string]bool{
		"p": true, "div": true, "ul": true, "ol": true, "table": true, "tr": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true,
	}
)

// htmlToText flattens teacher-authored HTML to plain text, keeping paragraph
// and list breaks so tone and structure survive.
func htmlToText(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			out := spaceRuns.ReplaceAllString(b.String(), " ")
			out = blankLines.ReplaceAllString(out, "\n\n")
			return strings.TrimSpace(out)
		case html.TextToken:
			b.Write(z.Text())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch tag := string(name); {
			case tag == "br":
				b.WriteString("\n")
			case tag == "li" && tt == html.StartTagToken:
				b.WriteString("\n- ")
			case blockBreaks[tag]:
				b.WriteString("\n\n")
			}
		}
	}
}
