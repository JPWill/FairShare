package discord

import (
	"strings"
	"unicode/utf8"
)

// codeTable renders rows as a monospace table inside a code block, trimmed to fit a
// Discord message.
func codeTable(header []string, rows [][]string) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i := range header {
			if i < len(r) && utf8.RuneCountInString(r[i]) > widths[i] {
				widths[i] = utf8.RuneCountInString(r[i])
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i := range header {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString(c)
			b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+2))
		}
		return strings.TrimRight(b.String(), " ")
	}
	var b strings.Builder
	b.WriteString("```\n")
	b.WriteString(line(header))
	b.WriteString("\n")
	total := 0
	for i := range header {
		total += widths[i] + 2
	}
	b.WriteString(strings.Repeat("-", total))
	b.WriteString("\n")
	for _, r := range rows {
		s := line(r) + "\n"
		if b.Len()+len(s) > 1900 {
			b.WriteString("...\n")
			break
		}
		b.WriteString(s)
	}
	b.WriteString("```")
	return b.String()
}
