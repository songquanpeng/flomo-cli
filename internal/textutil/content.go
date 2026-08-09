package textutil

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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

// UpdateHTMLTags edits hashtag text nodes while preserving the memo's existing
// HTML structure, links, lists, and formatting. Added tags are appended as a
// separate paragraph, matching flomo's editor representation.
func UpdateHTMLTags(contentHTML string, add, remove []string) (string, error) {
	addTags, err := cleanTagInputs(add)
	if err != nil {
		return "", err
	}
	removeTags, err := cleanTagInputs(remove)
	if err != nil {
		return "", err
	}
	removeSet := make(map[string]struct{}, len(removeTags))
	for _, tag := range removeTags {
		removeSet[tag] = struct{}{}
	}
	for _, tag := range addTags {
		if _, exists := removeSet[tag]; exists {
			return "", fmt.Errorf("tag %q cannot be added and removed in the same edit", tag)
		}
	}

	container := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(contentHTML), container)
	if err != nil {
		return "", fmt.Errorf("parse memo HTML: %w", err)
	}
	for _, node := range nodes {
		removeTagsFromNode(node, removeTags)
	}

	var current strings.Builder
	for _, node := range nodes {
		if err := xhtml.Render(&current, node); err != nil {
			return "", fmt.Errorf("render memo HTML: %w", err)
		}
	}
	existing := make(map[string]struct{})
	for _, tag := range ExtractTags(PlainText(current.String())) {
		existing[tag] = struct{}{}
	}
	toAdd := make([]string, 0, len(addTags))
	for _, tag := range addTags {
		if _, exists := existing[tag]; exists {
			continue
		}
		existing[tag] = struct{}{}
		toAdd = append(toAdd, "#"+tag)
	}
	if len(toAdd) > 0 {
		paragraph := &xhtml.Node{Type: xhtml.ElementNode, Data: "p", DataAtom: atom.P}
		paragraph.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: strings.Join(toAdd, " ")})
		nodes = append(nodes, paragraph)
	}

	var output strings.Builder
	for _, node := range nodes {
		if err := xhtml.Render(&output, node); err != nil {
			return "", fmt.Errorf("render memo HTML: %w", err)
		}
	}
	return output.String(), nil
}

func cleanTagInputs(tags []string) ([]string, error) {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if tag == "" || len(strings.Fields(tag)) != 1 || strings.Contains(tag, "#") {
			return nil, fmt.Errorf("invalid tag %q", raw)
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func removeTagsFromNode(node *xhtml.Node, tags []string) {
	if node.Type == xhtml.TextNode {
		removeSet := make(map[string]struct{}, len(tags))
		for _, tag := range tags {
			removeSet[tag] = struct{}{}
		}
		node.Data = tagPattern.ReplaceAllStringFunc(node.Data, func(match string) string {
			parts := tagPattern.FindStringSubmatch(match)
			if len(parts) != 2 {
				return match
			}
			if _, remove := removeSet[parts[1]]; !remove {
				return match
			}
			return strings.TrimSuffix(match, "#"+parts[1])
		})
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		removeTagsFromNode(child, tags)
	}
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
