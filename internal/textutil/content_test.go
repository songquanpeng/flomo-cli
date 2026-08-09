package textutil

import (
	"reflect"
	"testing"
)

func TestPlainTextAndTags(t *testing.T) {
	html := `<p>关于 AI 的想法 <a>#人工智能</a></p><p>第二行 #写作/方法。</p>`
	if got, want := PlainText(html), "关于 AI 的想法 #人工智能\n第二行 #写作/方法。"; got != want {
		t.Fatalf("PlainText() = %q, want %q", got, want)
	}
	wantTags := []string{"人工智能", "写作/方法"}
	if got := ExtractTags(PlainText(html)); !reflect.DeepEqual(got, wantTags) {
		t.Fatalf("ExtractTags() = %#v, want %#v", got, wantTags)
	}
}

func TestToHTML(t *testing.T) {
	if got, want := ToHTML("a < b\n#tag"), "<p>a &lt; b</p><p>#tag</p>"; got != want {
		t.Fatalf("ToHTML() = %q, want %q", got, want)
	}
}
