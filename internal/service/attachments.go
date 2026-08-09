package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/songquanpeng/flomo-cli/internal/storage"
)

const attachmentWorkers = 4

type AttachmentService struct {
	store     *storage.Store
	directory string
	client    *http.Client
}

type AttachmentRecord struct {
	MemoSlug      string  `json:"memo_slug"`
	ID            string  `json:"id,omitempty"`
	Type          string  `json:"type,omitempty"`
	Name          string  `json:"name,omitempty"`
	Content       string  `json:"content,omitempty"`
	URL           string  `json:"url,omitempty"`
	LocalPath     string  `json:"local_path,omitempty"`
	SHA256        string  `json:"sha256,omitempty"`
	DownloadedAt  string  `json:"downloaded_at,omitempty"`
	DownloadError string  `json:"download_error,omitempty"`
	Size          int64   `json:"size,omitempty"`
	Seconds       float64 `json:"seconds,omitempty"`
}

type AttachmentError struct {
	MemoSlug string `json:"memo_slug"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Error    string `json:"error"`
}

type AttachmentSyncResult struct {
	Total      int               `json:"total"`
	Downloaded int               `json:"downloaded"`
	Skipped    int               `json:"skipped"`
	Failed     int               `json:"failed"`
	Bytes      int64             `json:"bytes"`
	Directory  string            `json:"directory"`
	Errors     []AttachmentError `json:"errors,omitempty"`
}

func NewAttachmentService(store *storage.Store, directory string) *AttachmentService {
	return &AttachmentService{
		store:     store,
		directory: directory,
		client:    &http.Client{Timeout: 2 * time.Minute},
	}
}

func (s *AttachmentService) List(ctx context.Context, slug string) ([]AttachmentRecord, error) {
	memos, err := s.store.ListMemosWithFiles(ctx, slug)
	if err != nil {
		return nil, err
	}
	result := make([]AttachmentRecord, 0)
	for _, memo := range memos {
		files, err := attachmentMaps(memo.Files)
		if err != nil {
			return nil, fmt.Errorf("decode attachments for %s: %w", memo.Slug, err)
		}
		for _, file := range files {
			result = append(result, attachmentRecord(memo.Slug, file))
		}
	}
	return result, nil
}

func (s *AttachmentService) Sync(ctx context.Context, slug string) (AttachmentSyncResult, error) {
	result := AttachmentSyncResult{Directory: s.directory, Errors: []AttachmentError{}}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return result, fmt.Errorf("create attachment directory: %w", err)
	}
	memos, err := s.store.ListMemosWithFiles(ctx, slug)
	if err != nil {
		return result, err
	}
	type memoFiles struct {
		slug  string
		files []map[string]any
	}
	type job struct {
		memoSlug string
		file     map[string]any
	}
	groups := make([]memoFiles, 0, len(memos))
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	worker := func() {
		defer wg.Done()
		for item := range jobs {
			bytes, skipped, err := s.download(ctx, item.memoSlug, item.file)
			mu.Lock()
			if err != nil {
				result.Failed++
				record := attachmentRecord(item.memoSlug, item.file)
				result.Errors = append(result.Errors, AttachmentError{MemoSlug: item.memoSlug, ID: record.ID, Name: record.Name, Error: err.Error()})
			} else if skipped {
				result.Skipped++
			} else {
				result.Downloaded++
				result.Bytes += bytes
			}
			mu.Unlock()
		}
	}
	for range attachmentWorkers {
		wg.Add(1)
		go worker()
	}
	for _, memo := range memos {
		files, err := attachmentMaps(memo.Files)
		if err != nil {
			close(jobs)
			wg.Wait()
			return result, fmt.Errorf("decode attachments for %s: %w", memo.Slug, err)
		}
		groups = append(groups, memoFiles{slug: memo.Slug, files: files})
		for _, file := range files {
			result.Total++
			jobs <- job{memoSlug: memo.Slug, file: file}
		}
	}
	close(jobs)
	wg.Wait()
	for _, group := range groups {
		raw, err := json.Marshal(group.files)
		if err != nil {
			return result, fmt.Errorf("encode attachments for %s: %w", group.slug, err)
		}
		if err := s.store.UpdateMemoFiles(ctx, group.slug, raw); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *AttachmentService) download(ctx context.Context, memoSlug string, file map[string]any) (int64, bool, error) {
	if localPath := stringValue(file["local_path"]); localPath != "" {
		if info, err := os.Stat(localPath); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			delete(file, "download_error")
			return 0, true, nil
		}
	}
	remoteURL := stringValue(file["url"])
	if remoteURL == "" {
		err := errors.New("attachment URL is empty; run `flomo sync --full` to refresh its metadata")
		file["download_error"] = err.Error()
		return 0, false, err
	}
	target := s.targetPath(memoSlug, file)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		file["download_error"] = err.Error()
		return 0, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		file["download_error"] = err.Error()
		return 0, false, err
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36")
	request.Header.Set("Accept", "*/*")
	response, err := s.client.Do(request)
	if err != nil {
		file["download_error"] = err.Error()
		return 0, false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = fmt.Errorf("attachment HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusForbidden {
			err = errors.New("attachment HTTP 403; run `flomo sync --full` to refresh its signed URL")
		}
		file["download_error"] = err.Error()
		return 0, false, err
	}
	if filepath.Ext(target) == "" {
		if extensions, _ := mime.ExtensionsByType(response.Header.Get("Content-Type")); len(extensions) > 0 {
			target += extensions[0]
		}
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".download-*")
	if err != nil {
		file["download_error"] = err.Error()
		return 0, false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), response.Body)
	closeErr := temporary.Close()
	if copyErr != nil {
		err = copyErr
	} else if closeErr != nil {
		err = closeErr
	} else if err = os.Chmod(temporaryPath, 0o600); err == nil {
		err = os.Rename(temporaryPath, target)
	}
	if err != nil {
		file["download_error"] = err.Error()
		return 0, false, err
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		absolute = target
	}
	file["local_path"] = absolute
	file["sha256"] = hex.EncodeToString(hash.Sum(nil))
	file["downloaded_at"] = time.Now().UTC().Format(time.RFC3339)
	delete(file, "download_error")
	return written, false, nil
}

func (s *AttachmentService) targetPath(memoSlug string, file map[string]any) string {
	rawID := stringValue(file["id"])
	id := ""
	if rawID == "" {
		sum := sha256.Sum256([]byte(stringValue(file["url"])))
		id = hex.EncodeToString(sum[:6])
	} else {
		id = sanitizePathPart(rawID)
	}
	name := sanitizeFilename(stringValue(file["name"]))
	if name == "" {
		if parsed, err := url.Parse(stringValue(file["url"])); err == nil {
			name = sanitizeFilename(filepath.Base(parsed.Path))
		}
	}
	if name == "" || name == "." {
		name = "attachment"
	}
	return filepath.Join(s.directory, sanitizePathPart(memoSlug), id+"-"+name)
}

func attachmentMaps(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "[]" {
		return []map[string]any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var files []map[string]any
	if err := decoder.Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

func attachmentRecord(slug string, file map[string]any) AttachmentRecord {
	return AttachmentRecord{
		MemoSlug: slug, ID: stringValue(file["id"]), Type: stringValue(file["type"]),
		Name: stringValue(file["name"]), Content: stringValue(file["content"]), URL: stringValue(file["url"]),
		LocalPath: stringValue(file["local_path"]), SHA256: stringValue(file["sha256"]),
		DownloadedAt: stringValue(file["downloaded_at"]), DownloadError: stringValue(file["download_error"]), Size: int64Value(file["size"]),
		Seconds: float64Value(file["seconds"]),
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func int64Value(value any) int64 {
	number, _ := strconv.ParseInt(stringValue(value), 10, 64)
	return number
}

func float64Value(value any) float64 {
	number, _ := strconv.ParseFloat(stringValue(value), 64)
	return number
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	var output strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			output.WriteRune(r)
		} else {
			output.WriteByte('_')
		}
	}
	result := strings.Trim(output.String(), "._")
	if result == "" {
		return "unknown"
	}
	return result
}

func sanitizeFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	var output strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.() ", r) {
			output.WriteRune(r)
		} else {
			output.WriteByte('_')
		}
	}
	return strings.Trim(output.String(), " .")
}
