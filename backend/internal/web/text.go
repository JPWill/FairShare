package web

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// Shared text (content package) is plain: URLs stand alone and emphasis is **bold**.
// These two helpers turn that into safe HTML. Everything is escaped first; only the
// exact patterns below become markup.

var urlRe = regexp.MustCompile(`https://[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+`)

// linkify escapes text and wraps https URLs in anchors. A trailing full stop or comma
// stays outside the link.
func linkify(s string) template.HTML {
	esc := html.EscapeString(s)
	out := urlRe.ReplaceAllStringFunc(esc, func(u string) string {
		tail := ""
		for strings.HasSuffix(u, ".") || strings.HasSuffix(u, ",") || strings.HasSuffix(u, ")") {
			tail = u[len(u)-1:] + tail
			u = u[:len(u)-1]
		}
		return `<a href="` + u + `" rel="noopener">` + u + `</a>` + tail
	})
	return template.HTML(out)
}

// markdownBold escapes text and turns **x** into <strong>x</strong>.
func markdownBold(s string) template.HTML {
	esc := html.EscapeString(s)
	var b strings.Builder
	open := false
	for {
		i := strings.Index(esc, "**")
		if i < 0 {
			b.WriteString(esc)
			break
		}
		b.WriteString(esc[:i])
		if open {
			b.WriteString("</strong>")
		} else {
			b.WriteString("<strong>")
		}
		open = !open
		esc = esc[i+2:]
	}
	if open {
		b.WriteString("</strong>")
	}
	return template.HTML(b.String())
}
