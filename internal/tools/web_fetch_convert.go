package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

// extractJSON pretty-prints JSON content.
func extractJSON(body []byte) (string, string) {
	var data interface{}
	if err := json.Unmarshal(body, &data); err == nil {
		formatted, _ := json.MarshalIndent(data, "", "  ")
		return string(formatted), "json"
	}
	return string(body), "raw"
}

// --- HTML extraction utilities ---

var (
	reScript    = regexp.MustCompile(`(?is)<script[\s\S]*?</script>`)
	reStyle     = regexp.MustCompile(`(?is)<style[\s\S]*?</style>`)
	reComment   = regexp.MustCompile(`<!--[\s\S]*?-->`)
	reNav       = regexp.MustCompile(`(?is)<nav[\s\S]*?</nav>`)
	reFooter    = regexp.MustCompile(`(?is)<footer[\s\S]*?</footer>`)
	reHeader    = regexp.MustCompile(`(?is)<header[\s\S]*?</header>`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
	reMultiSP   = regexp.MustCompile(`[ \t]{2,}`)
	reH1        = regexp.MustCompile(`(?i)<h1[^>]*>([\s\S]*?)</h1>`)
	reH2        = regexp.MustCompile(`(?i)<h2[^>]*>([\s\S]*?)</h2>`)
	reH3        = regexp.MustCompile(`(?i)<h3[^>]*>([\s\S]*?)</h3>`)
	reH4        = regexp.MustCompile(`(?i)<h4[^>]*>([\s\S]*?)</h4>`)
	reH5        = regexp.MustCompile(`(?i)<h5[^>]*>([\s\S]*?)</h5>`)
	reH6        = regexp.MustCompile(`(?i)<h6[^>]*>([\s\S]*?)</h6>`)
	reParagraph = regexp.MustCompile(`(?i)<p[^>]*>([\s\S]*?)</p>`)
	reBreak     = regexp.MustCompile(`(?i)<br\s*/?>`)
	reListItem  = regexp.MustCompile(`(?i)<li[^>]*>([\s\S]*?)</li>`)
	reAnchor    = regexp.MustCompile(`(?i)<a[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
	rePre       = regexp.MustCompile(`(?is)<pre[^>]*>([\s\S]*?)</pre>`)
	reCode      = regexp.MustCompile(`(?i)<code[^>]*>([\s\S]*?)</code>`)
	reStrong    = regexp.MustCompile(`(?i)<(?:strong|b)[^>]*>([\s\S]*?)</(?:strong|b)>`)
	reEm        = regexp.MustCompile(`(?i)<(?:em|i)[^>]*>([\s\S]*?)</(?:em|i)>`)
	reBlockq    = regexp.MustCompile(`(?is)<blockquote[^>]*>([\s\S]*?)</blockquote>`)
	reImg       = regexp.MustCompile(`(?i)<img[^>]*alt="([^"]*)"[^>]*/?>`)
)

// htmlToMarkdown converts HTML to a markdown-like format.
// Not a full Readability implementation but covers common patterns.
func htmlToMarkdown(html string) string {
	// Remove non-content elements
	s := reScript.ReplaceAllString(html, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")
	s = reNav.ReplaceAllString(s, "")
	s = reFooter.ReplaceAllString(s, "")

	// Convert headings
	s = reH1.ReplaceAllString(s, "\n# $1\n")
	s = reH2.ReplaceAllString(s, "\n## $1\n")
	s = reH3.ReplaceAllString(s, "\n### $1\n")
	s = reH4.ReplaceAllString(s, "\n#### $1\n")
	s = reH5.ReplaceAllString(s, "\n##### $1\n")
	s = reH6.ReplaceAllString(s, "\n###### $1\n")

	// Pre/code blocks (before stripping other tags)
	s = rePre.ReplaceAllString(s, "\n```\n$1\n```\n")
	s = reCode.ReplaceAllString(s, "`$1`")

	// Blockquotes
	s = reBlockq.ReplaceAllStringFunc(s, func(match string) string {
		inner := reBlockq.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		lines := strings.Split(strings.TrimSpace(inner[1]), "\n")
		var quoted []string
		for _, l := range lines {
			quoted = append(quoted, "> "+strings.TrimSpace(l))
		}
		return "\n" + strings.Join(quoted, "\n") + "\n"
	})

	// Links: <a href="url">text</a> → [text](url)
	s = reAnchor.ReplaceAllString(s, "[$2]($1)")

	// Images: <img alt="text" ... /> → ![text]
	s = reImg.ReplaceAllString(s, "![$1]")

	// Bold/italic
	s = reStrong.ReplaceAllString(s, "**$1**")
	s = reEm.ReplaceAllString(s, "*$1*")

	// Paragraphs and breaks
	s = reParagraph.ReplaceAllString(s, "\n$1\n")
	s = reBreak.ReplaceAllString(s, "\n")

	// List items
	s = reListItem.ReplaceAllString(s, "\n- $1")

	// Strip remaining tags
	s = reTag.ReplaceAllString(s, "")

	// Clean up
	s = decodeHTMLEntities(s)
	s = reMultiNL.ReplaceAllString(s, "\n\n")
	s = reMultiSP.ReplaceAllString(s, " ")

	return strings.TrimSpace(s)
}

// htmlToText extracts plain text from HTML content.
func htmlToText(html string) string {
	s := reScript.ReplaceAllString(html, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")
	s = reNav.ReplaceAllString(s, "")
	s = reFooter.ReplaceAllString(s, "")
	s = reHeader.ReplaceAllString(s, "")

	// Structural breaks
	s = reParagraph.ReplaceAllString(s, "\n$1\n")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reListItem.ReplaceAllString(s, "\n- $1")

	// Strip all tags
	s = reTag.ReplaceAllString(s, "")

	s = decodeHTMLEntities(s)
	s = reMultiSP.ReplaceAllString(s, " ")
	s = reMultiNL.ReplaceAllString(s, "\n\n")

	// Clean lines
	lines := strings.Split(s, "\n")
	var clean []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}
	return strings.Join(clean, "\n")
}

// markdownToText strips markdown formatting for text mode.
func markdownToText(md string) string {
	s := md
	// Remove headers markers
	s = regexp.MustCompile(`(?m)^#{1,6}\s+`).ReplaceAllString(s, "")
	// Remove bold/italic markers
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	// Remove inline code
	s = regexp.MustCompile("`[^`]+`").ReplaceAllStringFunc(s, func(m string) string {
		return strings.Trim(m, "`")
	})
	// Remove links: [text](url) → text
	s = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(s, "$1")
	// Remove images
	s = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`).ReplaceAllString(s, "$1")
	// Clean whitespace
	s = reMultiNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// decodeHTMLEntities handles common HTML entities.
func decodeHTMLEntities(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
		"&mdash;", "\u2014",
		"&ndash;", "\u2013",
		"&laquo;", "\u00ab",
		"&raquo;", "\u00bb",
		"&bull;", "\u2022",
		"&hellip;", "...",
		"&copy;", "(c)",
		"&reg;", "(R)",
		"&trade;", "(TM)",
	)
	return replacer.Replace(s)
}
