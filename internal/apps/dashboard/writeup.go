package dashboard

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// The write-up is Markdown, the little of it the model is asked to use:
// headings, paragraphs, bullet and numbered lists, tables, bold, italic and
// code. Rendered here, line by line, into markup the card can trust: every
// character of the model's text is escaped first, and only the marks this
// renderer knows turn into tags. Nothing else -- no raw HTML, no links --
// gets through (spec 003, seventh amendment).

var (
	boldRe   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe = regexp.MustCompile(`(^|[^*])\*([^*\n]+?)\*`)
	codeRe   = regexp.MustCompile("`([^`]+)`")
	orderRe  = regexp.MustCompile(`^\d+[.)]\s+`)
	ruleRe   = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
)

// inline escapes a line and turns its bold, italic and code marks into tags.
func inline(s string) string {
	s = html.EscapeString(s)
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = italicRe.ReplaceAllString(s, "$1<em>$2</em>")
	return s
}

// renderWriteup turns the model's Markdown into the card's markup.
func renderWriteup(text string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string
	list := "" // "ul" or "ol" while inside one
	var table [][]string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		joined := strings.Join(para, " ")
		// A short lone line that does not end a sentence is a heading the
		// model wrote without a mark, as older write-ups did.
		if len(para) == 1 && len(joined) < 60 && !strings.HasSuffix(joined, ".") && !strings.HasSuffix(joined, ":") {
			b.WriteString("<h4>" + inline(joined) + "</h4>\n")
		} else {
			b.WriteString("<p>" + inline(joined) + "</p>\n")
		}
		para = nil
	}
	flushList := func() {
		if list != "" {
			b.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		b.WriteString("<table class=\"writeup-table\">\n")
		for i, row := range table {
			cell := "td"
			if i == 0 {
				cell = "th"
				b.WriteString("<thead>\n")
			}
			b.WriteString("<tr>")
			for _, c := range row {
				b.WriteString("<" + cell + ">" + inline(c) + "</" + cell + ">")
			}
			b.WriteString("</tr>\n")
			if i == 0 {
				b.WriteString("</thead>\n<tbody>\n")
			}
		}
		b.WriteString("</tbody>\n</table>\n")
		table = nil
	}
	flushAll := func() { flushPara(); flushList(); flushTable() }

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flushAll()
		case strings.HasPrefix(line, "#"):
			flushAll()
			level := 0
			for level < len(line) && line[level] == '#' {
				level++
			}
			tag := "h4"
			if level >= 3 {
				tag = "h5"
			}
			b.WriteString("<" + tag + ">" + inline(strings.TrimSpace(line[level:])) + "</" + tag + ">\n")
		case strings.HasPrefix(line, "|"):
			flushPara()
			flushList()
			if ruleRe.MatchString(line) {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			table = append(table, cells)
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			flushPara()
			flushTable()
			if list != "ul" {
				flushList()
				list = "ul"
				b.WriteString("<ul>\n")
			}
			b.WriteString("<li>" + inline(strings.TrimSpace(line[2:])) + "</li>\n")
		case orderRe.MatchString(line):
			flushPara()
			flushTable()
			if list != "ol" {
				flushList()
				list = "ol"
				b.WriteString("<ol>\n")
			}
			b.WriteString("<li>" + inline(orderRe.ReplaceAllString(line, "")) + "</li>\n")
		default:
			flushList()
			flushTable()
			para = append(para, line)
		}
	}
	flushAll()
	return template.HTML(b.String()) //nolint:gosec // every character was escaped by inline; only this renderer's tags remain
}
