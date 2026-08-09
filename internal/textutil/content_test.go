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

func TestUpdateHTMLTagsPreservesFormatting(t *testing.T) {
	input := `<p><strong>Idea</strong> #old</p><ul><li>item #keep</li></ul>`
	got, err := UpdateHTMLTags(input, []string{"new", "keep"}, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<p><strong>Idea</strong> </p><ul><li>item #keep</li></ul><p>#new</p>`
	if got != want {
		t.Fatalf("UpdateHTMLTags() = %q, want %q", got, want)
	}
}

func TestUpdateHTMLTagsRejectsConflicts(t *testing.T) {
	if _, err := UpdateHTMLTags(`<p>#same</p>`, []string{"same"}, []string{"same"}); err == nil {
		t.Fatal("expected conflicting tag edit to fail")
	}
}

func TestUpdateHTMLTagsRemovesOnlyExactHierarchicalTag(t *testing.T) {
	got, err := UpdateHTMLTags(`<p>#工作 #工作/项目</p>`, nil, []string{"工作"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p> #工作/项目</p>`; got != want {
		t.Fatalf("UpdateHTMLTags() = %q, want %q", got, want)
	}
}
