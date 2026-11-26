package stories

import (
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://[^\s<>"]+`)

// seg is one piece of a text: plain text, a link or a line break. Every key is
// always present so templates never meet a missing one.
func seg(text, href string, br bool) map[string]any {
	return map[string]any{"text": text, "href": href, "br": br}
}

// textSegments splits plain text into text, link and line-break segments.
// Only http(s) URLs become links; trailing punctuation stays text. The result
// holds no HTML: escaping is the template's job.
func textSegments(s string) []map[string]any {
	s = strings.ReplaceAll(s, "\r", "")
	var out []map[string]any
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			out = append(out, seg("", "", true))
		}
		rest := line
		for _, m := range urlRe.FindAllStringIndex(line, -1) {
			link := trimLink(line[m[0]:m[1]])
			end := m[0] + len(link)
			if link == "" || end <= m[0] {
				continue
			}
			if pre := line[len(line)-len(rest) : m[0]]; pre != "" {
				out = append(out, seg(pre, "", false))
			}
			out = append(out, seg(link, link, false))
			rest = line[end:]
		}
		if rest != "" {
			out = append(out, seg(rest, "", false))
		}
	}
	return out
}

// trimLink drops trailing .,;:!? and a ')' that has no '(' to close inside the link.
func trimLink(l string) string {
	for l != "" {
		c := l[len(l)-1]
		if strings.IndexByte(".,;:!?", c) >= 0 {
			l = l[:len(l)-1]
			continue
		}
		if c == ')' && strings.Count(l, "(") < strings.Count(l, ")") {
			l = l[:len(l)-1]
			continue
		}
		break
	}
	return l
}
