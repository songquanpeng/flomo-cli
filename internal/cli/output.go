package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/songquanpeng/flomo-cli/internal/storage"
)

func writeJSON(writer io.Writer, value any, indent bool) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}

func writeMemoText(writer io.Writer, memo storage.Memo, maxChars int) {
	tags := ""
	if len(memo.Tags) > 0 {
		hashTags := make([]string, 0, len(memo.Tags))
		for _, tag := range memo.Tags {
			hashTags = append(hashTags, "#"+tag)
		}
		tags = "  " + strings.Join(hashTags, " ")
	}
	fmt.Fprintf(writer, "%s  %s%s\n", memo.Slug, memo.CreatedAt, tags)
	fmt.Fprintln(writer, truncate(memo.Content, maxChars))
}

func truncate(value string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(value) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "…"
}

func stripHTMLField(memos []storage.Memo, include bool, maxChars int) []storage.Memo {
	result := make([]storage.Memo, len(memos))
	copy(result, memos)
	for index := range result {
		if !include {
			result[index].ContentHTML = ""
		}
		result[index].Content = truncate(result[index].Content, maxChars)
	}
	return result
}
