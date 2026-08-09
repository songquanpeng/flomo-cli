package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/songquanpeng/flomo-cli/internal/storage"
)

func TestAttachmentSyncDownloadsAndPersistsLocalPath(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		writer.Header().Set("Content-Type", "audio/mp4")
		_, _ = writer.Write([]byte("audio-data"))
	}))
	defer server.Close()

	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	files := json.RawMessage(`[{"id":7,"type":"recorded","name":"voice.m4a","size":10,"url":"` + server.URL + `/voice"}]`)
	if err := store.UpsertMemos(ctx, []storage.Memo{{Slug: "memo/unsafe", Content: "voice", Files: files}}); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "attachments")
	service := NewAttachmentService(store, directory)
	result, err := service.Sync(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Downloaded != 1 || result.Failed != 0 || result.Bytes != 10 {
		t.Fatalf("result = %#v", result)
	}
	items, err := service.List(ctx, "memo/unsafe")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !filepath.IsAbs(items[0].LocalPath) || strings.Contains(items[0].LocalPath, "memo/unsafe") {
		t.Fatalf("items = %#v", items)
	}
	content, err := os.ReadFile(items[0].LocalPath)
	if err != nil || string(content) != "audio-data" {
		t.Fatalf("downloaded content = %q, err = %v", content, err)
	}

	result, err = service.Sync(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped != 1 || requests.Load() != 1 {
		t.Fatalf("second result = %#v, requests = %d", result, requests.Load())
	}

	if err := store.UpsertMemos(ctx, []storage.Memo{{Slug: "memo/unsafe", Content: "updated", Files: files}}); err != nil {
		t.Fatal(err)
	}
	items, err = service.List(ctx, "memo/unsafe")
	if err != nil || len(items) != 1 || items[0].LocalPath == "" {
		t.Fatalf("metadata after remote upsert = %#v, err = %v", items, err)
	}
}
