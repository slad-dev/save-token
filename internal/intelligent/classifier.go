package intelligent

import (
	"regexp"
	"sort"
	"strings"
)

var realtimePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(latest|today|current|recent|breaking|news|price|weather|stock|rate|release date|earnings)\b`),
	regexp.MustCompile(`(?i)(今天|最新|刚刚|实时|当前|最近|新闻|股价|天气|汇率|热搜|比分|上市|发布)`),
	regexp.MustCompile(`\b20\d{2}\b`),
}

var stopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "what": {}, "who": {}, "when": {}, "where": {},
	"how": {}, "please": {}, "tell": {}, "about": {}, "and": {}, "for": {}, "with": {}, "that": {},
	"这个": {}, "那个": {}, "请": {}, "一下": {}, "帮我": {}, "什么": {}, "怎么": {}, "是否": {},
}

func needsRealtimeWebSearch(input string) bool {
	normalized := strings.TrimSpace(input)
	if normalized == "" {
		return false
	}

	for _, pattern := range realtimePatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}

	return strings.Contains(normalized, "联网") || strings.Contains(normalized, "搜索")
}

func extractKeywords(input string) string {
	fields := strings.FieldsFunc(strings.ToLower(input), func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r >= 0x4e00 && r <= 0x9fa5:
			return false
		default:
			return true
		}
	})

	counts := make(map[string]int)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ignored := stopwords[field]; ignored {
			continue
		}
		counts[field]++
	}

	type pair struct {
		key   string
		count int
	}

	pairs := make([]pair, 0, len(counts))
	for key, count := range counts {
		pairs = append(pairs, pair{key: key, count: count})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return len([]rune(pairs[i].key)) > len([]rune(pairs[j].key))
		}
		return pairs[i].count > pairs[j].count
	})

	limit := 8
	if len(pairs) < limit {
		limit = len(pairs)
	}

	selected := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		selected = append(selected, pairs[i].key)
	}

	if len(selected) == 0 {
		return strings.TrimSpace(input)
	}

	return strings.Join(selected, " ")
}
