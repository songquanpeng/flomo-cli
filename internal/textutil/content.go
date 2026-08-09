package textutil

import (
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
)

var tagPattern = regexp.MustCompile(`(?:^|[\s\p{P}])#([^#\s<>{}\[\]，。！？；：、]+)`)

func PlainText(contentHTML string) string {
	doc, err := xhtml.Parse(strings.NewReader(contentHTML))
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(stripTags(contentHTML)))
	}
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			builder.WriteString(node.Data)
		}
		if node.Type == xhtml.ElementNode && node.Data == "br" {
			builder.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == xhtml.ElementNode && isBlock(node.Data) {
			builder.WriteByte('\n')
		}
	}
	walk(doc)
	return normalizeText(builder.String())
}

func ExtractTags(text string) []string {
	matches := tagPattern.FindAllStringSubmatch(text, -1)
	seen := make(map[string]struct{}, len(matches))
	tags := make([]string, 0, len(matches))
	for _, match := range matches {
		tag := strings.TrimSpace(strings.TrimRightFunc(match[1], unicode.IsPunct))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func ToHTML(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	paragraphs := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			paragraphs = append(paragraphs, "<p><br></p>")
		} else {
			paragraphs = append(paragraphs, "<p>"+html.EscapeString(line)+"</p>")
		}
	}
	return strings.Join(paragraphs, "")
}

func normalizeText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\u00a0", " "), "\n")
	out := make([]string, 0, len(lines))
	lastBlank := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !lastBlank {
				out = append(out, "")
			}
			lastBlank = true
			continue
		}
		out = append(out, line)
		lastBlank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func isBlock(tag string) bool {
	switch tag {
	case "p", "div", "li", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

func stripTags(value string) string {
	var builder strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return builder.String()
}
