package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/songquanpeng/flomo-cli/internal/domain"
	"github.com/songquanpeng/flomo-cli/internal/storage"
)

type memoGatewayStub struct {
	editRequest domain.EditMemoRequest
}

func (g *memoGatewayStub) UpdatedMemos(context.Context, int, int64, string, string) ([]domain.Memo, error) {
	return nil, nil
}

func (g *memoGatewayStub) AddMemo(context.Context, domain.AddMemoRequest) (domain.Memo, error) {
	return domain.Memo{}, nil
}

func (g *memoGatewayStub) EditMemo(_ context.Context, slug string, request domain.EditMemoRequest) (domain.Memo, error) {
	g.editRequest = request
	return domain.Memo{Slug: slug, Content: request.Content, CreatedAt: request.CreatedAt, Files: json.RawMessage(`[{"id":42}]`)}, nil
}

func TestWebTimezoneMatchesFlomoWebProtocol(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		zone *time.Location
		want string
	}{
		{"east eight", time.FixedZone("east", 8*3600), "8:0"},
		{"east five thirty", time.FixedZone("east", 5*3600+30*60), "5:30"},
		{"west five thirty", time.FixedZone("west", -(5*3600 + 30*60)), "-5:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webTimezone(tt.zone, at); got != tt.want {
				t.Fatalf("webTimezone() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEditPreservesRichTextAttachmentsAndAddsTag(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertMemos(ctx, []storage.Memo{{
		Slug: "memo-1", ContentHTML: `<p><strong>正文</strong> #旧标签</p>`, Content: "正文 #旧标签",
		Tags: []string{"旧标签"}, CreatedAt: "2026-08-01 12:00:00", MemoFrom: "web", Source: "web",
		Pinned: true, Files: json.RawMessage(`[{"id":42,"local_path":"/tmp/cached.m4a"}]`),
	}}); err != nil {
		t.Fatal(err)
	}
	gateway := &memoGatewayStub{}
	edited, err := NewMemoService(gateway, store, "Asia/Shanghai").Edit(ctx, "memo-1", nil, false, []string{"新标签"}, []string{"旧标签"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := gateway.editRequest.Content, `<p><strong>正文</strong> </p><p>#新标签</p>`; got != want {
		t.Fatalf("edited HTML = %q, want %q", got, want)
	}
	if len(gateway.editRequest.FileIDs) != 1 || gateway.editRequest.FileIDs[0].(json.Number).String() != "42" {
		t.Fatalf("file IDs = %#v", gateway.editRequest.FileIDs)
	}
	if !gateway.editRequest.Pinned {
		t.Fatal("pin state was not preserved")
	}
	if got := string(edited.Files); got != `[{"id":42,"local_path":"/tmp/cached.m4a"}]` {
		t.Fatalf("local attachment metadata = %s", got)
	}
}
