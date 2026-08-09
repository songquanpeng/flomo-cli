package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchSupportsChineseSubstringsTagsAndDeletion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	memos := []Memo{
		{Slug: "new", Content: "用人工智能辅助写作", Tags: []string{"AI/写作"}, CreatedUnix: 20, UpdatedUnix: 20},
		{Slug: "old", Content: "人工智能与学习", Tags: []string{"AI"}, CreatedUnix: 10, UpdatedUnix: 10},
		{Slug: "deleted", Content: "人工智能旧稿", CreatedUnix: 30, UpdatedUnix: 30, DeletedAt: "2025-01-01 00:00:00"},
	}
	if err := store.UpsertMemos(ctx, memos); err != nil {
		t.Fatal(err)
	}
	result, err := store.Search(ctx, SearchOptions{Query: "人工智能", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	short, err := store.Search(ctx, SearchOptions{Query: "写作", Tags: []string{"AI"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if short.Total != 1 || short.Memos[0].Slug != "new" {
		t.Fatalf("short/tag search = %#v, want new", short)
	}
	mixed, err := store.Search(ctx, SearchOptions{Query: "人工智能 写作", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if mixed.Total != 1 || mixed.Memos[0].Slug != "new" {
		t.Fatalf("mixed trigram/literal search = %#v, want new", mixed)
	}
}

func TestUpsertRefreshesFullTextIndex(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertMemos(ctx, []Memo{{Slug: "one", Content: "original topic", UpdatedUnix: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMemos(ctx, []Memo{{Slug: "one", Content: "replacement idea", UpdatedUnix: 2}}); err != nil {
		t.Fatal(err)
	}
	old, err := store.Search(ctx, SearchOptions{Query: "original", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if old.Total != 0 {
		t.Fatalf("old index entry remains: %#v", old)
	}
	updated, err := store.Search(ctx, SearchOptions{Query: "replacement", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Total != 1 {
		t.Fatalf("updated index missing: %#v", updated)
	}
}
