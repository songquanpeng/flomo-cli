package cli

import "testing"

func TestBuildNextPageCommandPreservesFilters(t *testing.T) {
	flags := searchFlags{
		tags:           []string{"Work", "AI/LLM"},
		from:           "2026-01-01",
		to:             "2026-06-30",
		limit:          20,
		any:            true,
		includeDeleted: true,
		includeHTML:    true,
		output:         "json",
		maxChars:       500,
	}
	got := buildNextPageCommand("search", "agent's design", flags, 40)
	want := "flomo search 'agent'\"'\"'s design' --tag 'Work' --tag 'AI/LLM' --from '2026-01-01' --to '2026-06-30' --any --include-deleted --include-html --max-chars 500 --output json --limit 20 --offset 40"
	if got != want {
		t.Fatalf("next command:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildNextListPageIsImmediatelyRunnable(t *testing.T) {
	flags := searchFlags{limit: 20, sort: "newest", output: "text"}
	if got, want := buildNextPageCommand("list", "", flags, 20), "flomo list --limit 20 --offset 20"; got != want {
		t.Fatalf("next command = %q, want %q", got, want)
	}
}
