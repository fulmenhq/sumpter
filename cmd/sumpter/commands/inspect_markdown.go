package commands

import (
	"fmt"
	"strings"
)

// mdVisible renders characters that would break a Markdown line or reorder
// its display as visible escapes: \n, \r and \t; other C0 and C1 controls
// and DEL as \xNN; line and paragraph separators, bidirectional controls and
// the byte order mark as \uXXXX.
func mdVisible(s string) string {
	if !strings.ContainsFunc(s, needsVisible) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02X`, r)
		case needsVisible(r):
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsVisible(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, 0x80 <= r && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r == 0x2028, r == 0x2029,
		0x202a <= r && r <= 0x202e, 0x2066 <= r && r <= 0x2069, r == 0xfeff:
		return true
	}
	return false
}

// mdText renders s as inert Markdown text for a table cell or heading: it is
// made visible, HTML-significant characters become entities, and Markdown
// punctuation that XML names never contain is backslash-escaped, so a table
// row keeps its cells and no link, code span or HTML can start inside s.
func mdText(s string) string {
	s = mdVisible(s)
	if !strings.ContainsAny(s, "\\`*[]|~&<>") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\\', '`', '*', '[', ']', '|', '~':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mdCode renders s as a Markdown code span, whose content is literal: the
// fence is one backtick longer than the longest run of backticks in s, and the
// content is padded with a space where the span would otherwise lose or
// misread its edges.
func mdCode(s string) string {
	s = mdVisible(s)
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	pad := strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") ||
		len(s) >= 2 && s[0] == ' ' && s[len(s)-1] == ' ' && strings.Trim(s, " ") != ""
	if pad {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}
