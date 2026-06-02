package main

import (
	"regexp"
	"strings"
)

var sqlKeywordRE = regexp.MustCompile(`(?i)\b(select|insert|update|delete|from|where|join|left|right|inner|outer|on|group|by|order|having|limit|offset|create|alter|drop|table|view|index|function|procedure|foreign|key|references|primary|unique|not|null|and|or|as|with|case|when|then|else|end|values|set|into)\b`)

func highlightSQLView(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			prefix := sqlKeywordRE.ReplaceAllStringFunc(line[:idx], func(match string) string {
				return sqlKeywordStyle.Render(match)
			})
			lines[i] = prefix + sqlCommentStyle.Render(line[idx:])
			continue
		}
		lines[i] = sqlKeywordRE.ReplaceAllStringFunc(line, func(match string) string {
			return sqlKeywordStyle.Render(match)
		})
	}
	return strings.Join(lines, "\n")
}
