package card

import "strings"

// linkTextReplacer implements the Feishu "特殊字符转义说明" entity table plus the
// line-break folding. It is the only place where link text is escaped: a title
// that still carries markdown syntax would either be swallowed by the parser or
// break the link out of its `[...]` (tech design v0.2 §7.3.4).
//
// strings.Replacer never re-scans its own output, so `&` may be mapped to
// `&amp;` without double-escaping the entities produced below.
var linkTextReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"[", "&#91;",
	"]", "&#93;",
	"(", "&#40;",
	")", "&#41;",
	"*", "&#42;",
	"_", "&#95;",
	"~", "&sim;",
	"`", "&#96;",
	"\\", "&#92;",
	"\r\n", " ",
	"\r", " ",
	"\n", " ",
)

// escapeLinkText escapes one display name for a markdown element and folds every
// line break into a space, so one attachment can never occupy two lines.
func escapeLinkText(s string) string {
	return linkTextReplacer.Replace(s)
}

// displayTitle is the text shown for one attachment: the material title, or the
// material id when the title is empty (same rule as the Web attachment area).
func displayTitle(link Link) string {
	if link.Title != "" {
		return link.Title
	}
	return link.MaterialID
}

// renderLinks renders one line per attachment, in the order of links (which is
// the Web attachment order). A link with an empty URL degrades to its bare
// file name: no placeholder, no error copy — the reviewer still sees that the
// file exists and the other lines stay clickable.
func renderLinks(links []Link) string {
	if len(links) == 0 {
		return ""
	}
	lines := make([]string, 0, len(links))
	for _, link := range links {
		title := escapeLinkText(displayTitle(link))
		// The URL is deliberately not escaped: it is not parsed as text.
		if link.URL == "" {
			lines = append(lines, title)
			continue
		}
		lines = append(lines, "["+title+"]("+link.URL+")")
	}
	return strings.Join(lines, "\n")
}
