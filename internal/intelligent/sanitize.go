package intelligent

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	htmlTagPattern        = regexp.MustCompile(`(?is)<[^>]+>`)
	markdownLinkPattern   = regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)
	markdownImagePattern  = regexp.MustCompile(`!\[(.*?)\]\((.*?)\)`)
	markdownFencePattern  = regexp.MustCompile("(?s)```.*?```")
	markdownInlinePattern = regexp.MustCompile("`([^`]*)`")
	markdownHeaderPattern = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s*`)
	markdownListPattern   = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	markdownQuotePattern  = regexp.MustCompile(`(?m)^\s*>\s*`)
	markdownEmPattern     = regexp.MustCompile(`[*_~#]+`)
	whitespacePattern     = regexp.MustCompile(`\s+`)
)

func sanitizeSearchText(input string, limit int) string {
	text := strings.TrimSpace(input)
	if text == "" {
		return ""
	}

	text = html.UnescapeString(text)
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = markdownFencePattern.ReplaceAllString(text, " ")
	text = markdownImagePattern.ReplaceAllString(text, "$1")
	text = markdownLinkPattern.ReplaceAllString(text, "$1")
	text = markdownInlinePattern.ReplaceAllString(text, "$1")
	text = markdownHeaderPattern.ReplaceAllString(text, "")
	text = markdownListPattern.ReplaceAllString(text, "")
	text = markdownQuotePattern.ReplaceAllString(text, "")
	text = markdownEmPattern.ReplaceAllString(text, "")
	text = whitespacePattern.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	if limit > 0 && utf8.RuneCountInString(text) > limit {
		text = truncateRunes(text, limit)
	}

	return text
}

func truncateRunes(input string, limit int) string {
	if limit <= 0 {
		return input
	}

	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}

	return strings.TrimSpace(string(runes[:limit])) + "..."
}
