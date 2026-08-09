package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/songquanpeng/flomo-cli/internal/domain"
	"github.com/songquanpeng/flomo-cli/internal/storage"
	"github.com/songquanpeng/flomo-cli/internal/textutil"
)

type MemoService struct {
	client   MemoGateway
	store    *storage.Store
	timezone string // flomo web protocol format, for example "8:0" or "-5:30"
	location *time.Location
}

// MemoGateway is the stable port consumed by the application layer. The
// current flomo Web API client is one adapter; protocol changes stay outside
// this package.
type MemoGateway interface {
	UpdatedMemos(ctx context.Context, limit int, latestUpdatedAt int64, latestSlug, timezone string) ([]domain.Memo, error)
	AddMemo(ctx context.Context, request domain.AddMemoRequest) (domain.Memo, error)
	EditMemo(ctx context.Context, slug string, request domain.EditMemoRequest) (domain.Memo, error)
}

type SyncResult struct {
	Fetched      int    `json:"fetched"`
	Pages        int    `json:"pages"`
	Full         bool   `json:"full"`
	LastCursorAt int64  `json:"last_cursor_at"`
	LastSlug     string `json:"last_slug,omitempty"`
	SyncedAt     string `json:"synced_at"`
}

func NewMemoService(client MemoGateway, store *storage.Store, timezone string) *MemoService {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.Local
	}
	return &MemoService{client: client, store: store, timezone: webTimezone(location, time.Now()), location: location}
}

func (s *MemoService) Sync(ctx context.Context, full bool) (SyncResult, error) {
	const pageSize = 200
	var cursorAt int64
	var cursorSlug string
	if !full {
		value, err := s.store.State(ctx, "memo_cursor_updated_at")
		if err != nil {
			return SyncResult{}, err
		}
		if value != "" {
			cursorAt, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return SyncResult{}, fmt.Errorf("invalid local sync cursor: %w; run `flomo sync --full`", err)
			}
		}
		cursorSlug, err = s.store.State(ctx, "memo_cursor_slug")
		if err != nil {
			return SyncResult{}, err
		}
	}
	result := SyncResult{Full: full, LastCursorAt: cursorAt, LastSlug: cursorSlug}
	for page := 0; page < 100000; page++ {
		remoteMemos, err := s.client.UpdatedMemos(ctx, pageSize, cursorAt, cursorSlug, s.timezone)
		if err != nil {
			return result, fmt.Errorf("sync memo page %d: %w", page+1, err)
		}
		if len(remoteMemos) == 0 {
			break
		}
		localMemos := make([]storage.Memo, 0, len(remoteMemos))
		for _, memo := range remoteMemos {
			localMemos = append(localMemos, s.toStorageMemo(memo))
		}
		if err := s.store.UpsertMemos(ctx, localMemos); err != nil {
			return result, err
		}
		last := localMemos[len(localMemos)-1]
		if last.UpdatedUnix == 0 {
			return result, fmt.Errorf("memo %s has an unrecognized updated_at value %q", last.Slug, last.UpdatedAt)
		}
		if last.UpdatedUnix == cursorAt && last.Slug == cursorSlug {
			return result, errors.New("flomo sync cursor did not advance")
		}
		cursorAt, cursorSlug = last.UpdatedUnix, last.Slug
		if err := s.store.SetState(ctx, "memo_cursor_updated_at", strconv.FormatInt(cursorAt, 10)); err != nil {
			return result, err
		}
		if err := s.store.SetState(ctx, "memo_cursor_slug", cursorSlug); err != nil {
			return result, err
		}
		result.Fetched += len(localMemos)
		result.Pages++
		result.LastCursorAt = cursorAt
		result.LastSlug = cursorSlug
		if len(remoteMemos) < pageSize {
			break
		}
	}
	result.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.store.SetState(ctx, "last_synced_at", result.SyncedAt); err != nil {
		return result, err
	}
	return result, nil
}

func (s *MemoService) Add(ctx context.Context, plainText string, tags []string, createdAt time.Time, contentIsHTML bool) (storage.Memo, error) {
	plainText = strings.TrimSpace(plainText)
	if plainText == "" {
		return storage.Memo{}, errors.New("memo content is empty")
	}
	if utf8.RuneCountInString(plainText) > 30000 {
		return storage.Memo{}, errors.New("memo exceeds flomo's 30,000-character limit")
	}
	if !contentIsHTML {
		plainText = appendTags(plainText, tags)
	}
	contentHTML := plainText
	if !contentIsHTML {
		contentHTML = textutil.ToHTML(plainText)
	}
	remote, err := s.client.AddMemo(ctx, domain.AddMemoRequest{
		Content:   contentHTML,
		CreatedAt: createdAt.Unix(),
		MemoFrom:  "api",
		Source:    "flomo_cli",
		Timezone:  s.timezone,
	})
	if err != nil {
		return storage.Memo{}, err
	}
	if remote.Slug == "" {
		return storage.Memo{}, errors.New("flomo created a memo without returning its slug")
	}
	if remote.Content == "" {
		remote.Content = contentHTML
	}
	if remote.CreatedAt == "" {
		remote.CreatedAt = formatFlomoTime(createdAt.In(s.location))
	}
	if remote.UpdatedAt == "" {
		remote.UpdatedAt = remote.CreatedAt
	}
	memo := s.toStorageMemo(remote)
	if err := s.store.UpsertMemos(ctx, []storage.Memo{memo}); err != nil {
		return storage.Memo{}, fmt.Errorf("memo was added remotely but local caching failed: %w", err)
	}
	return memo, nil
}

func (s *MemoService) Edit(ctx context.Context, slug string, replacement *string, contentIsHTML bool, addTags, removeTags []string) (storage.Memo, error) {
	current, err := s.store.GetMemo(ctx, slug, false)
	if err != nil {
		return storage.Memo{}, err
	}
	contentHTML := current.ContentHTML
	if contentHTML == "" {
		contentHTML = textutil.ToHTML(current.Content)
	}
	if replacement != nil {
		value := strings.TrimSpace(*replacement)
		if value == "" {
			return storage.Memo{}, errors.New("replacement memo content is empty")
		}
		if utf8.RuneCountInString(value) > 30000 {
			return storage.Memo{}, errors.New("memo exceeds flomo's 30,000-character limit")
		}
		contentHTML = value
		if !contentIsHTML {
			contentHTML = textutil.ToHTML(value)
		}
	}
	contentHTML, err = textutil.UpdateHTMLTags(contentHTML, addTags, removeTags)
	if err != nil {
		return storage.Memo{}, err
	}
	if contentHTML == current.ContentHTML {
		return current, nil
	}
	fileIDs, err := attachmentFileIDs(current.Files)
	if err != nil {
		return storage.Memo{}, fmt.Errorf("read existing attachment IDs: %w", err)
	}
	source := current.Source
	if source == "" {
		source = "web"
	}
	remote, err := s.client.EditMemo(ctx, slug, domain.EditMemoRequest{
		Content:        contentHTML,
		CreatedAt:      current.CreatedAt,
		LocalUpdatedAt: time.Now().Unix(),
		MemoFrom:       current.MemoFrom,
		Source:         source,
		FileIDs:        fileIDs,
		Timezone:       s.timezone,
		Pinned:         current.Pinned,
	})
	if err != nil {
		return storage.Memo{}, err
	}
	if remote.Slug == "" {
		remote.Slug = slug
	}
	if remote.Content == "" {
		remote.Content = contentHTML
	}
	if remote.CreatedAt == "" {
		remote.CreatedAt = current.CreatedAt
	}
	if remote.UpdatedAt == "" {
		remote.UpdatedAt = formatFlomoTime(time.Now().In(s.location))
	}
	if remote.MemoFrom == "" {
		remote.MemoFrom = current.MemoFrom
	}
	if remote.Source == "" {
		remote.Source = source
	}
	if len(remote.Pin) == 0 || string(remote.Pin) == "null" {
		remote.Pin = json.RawMessage(strconv.Itoa(boolNumber(current.Pinned)))
	}
	if len(remote.Files) == 0 || string(remote.Files) == "null" {
		remote.Files = current.Files
	}
	edited := s.toStorageMemo(remote)
	if err := s.store.UpsertMemos(ctx, []storage.Memo{edited}); err != nil {
		return storage.Memo{}, fmt.Errorf("memo was edited remotely but local caching failed: %w", err)
	}
	cached, err := s.store.GetMemo(ctx, edited.Slug, false)
	if err != nil {
		return storage.Memo{}, fmt.Errorf("memo was edited remotely but local reload failed: %w", err)
	}
	return cached, nil
}

func (s *MemoService) toStorageMemo(memo domain.Memo) storage.Memo {
	text := textutil.PlainText(memo.Content)
	return storage.Memo{
		Slug:        memo.Slug,
		ContentHTML: memo.Content,
		Content:     text,
		Tags:        textutil.ExtractTags(text),
		CreatedAt:   memo.CreatedAt,
		CreatedUnix: parseFlomoTime(memo.CreatedAt, s.location),
		UpdatedAt:   memo.UpdatedAt,
		UpdatedUnix: parseFlomoTime(memo.UpdatedAt, s.location),
		DeletedAt:   memo.DeletedAt,
		MemoFrom:    memo.MemoFrom,
		Source:      memo.Source,
		Pinned:      parsePinned(memo.Pin),
		Files:       memo.Files,
	}
}

func appendTags(content string, tags []string) string {
	existing := make(map[string]struct{})
	for _, tag := range textutil.ExtractTags(content) {
		existing[tag] = struct{}{}
	}
	toAdd := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
		if tag == "" {
			continue
		}
		if _, ok := existing[tag]; ok {
			continue
		}
		existing[tag] = struct{}{}
		toAdd = append(toAdd, "#"+tag)
	}
	if len(toAdd) > 0 {
		content += "\n\n" + strings.Join(toAdd, " ")
	}
	return content
}

func parseFlomoTime(value string, location *time.Location) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return unix
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		var parsed time.Time
		var err error
		if strings.Contains(layout, "Z07") {
			parsed, err = time.Parse(layout, value)
		} else {
			parsed, err = time.ParseInLocation(layout, value, location)
		}
		if err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

func parsePinned(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value == "true" || (value != "" && value != "0" && value != "false" && value != "null")
}

func formatFlomoTime(value time.Time) string { return value.Format("2006-01-02 15:04:05") }

func attachmentFileIDs(raw json.RawMessage) ([]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var files []map[string]any
	if err := decoder.Decode(&files); err != nil {
		return nil, err
	}
	result := make([]any, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		id, exists := file["id"]
		if !exists || id == nil || fmt.Sprint(id) == "" || strings.HasPrefix(fmt.Sprint(id), "local_") {
			continue
		}
		key := fmt.Sprint(id)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

func webTimezone(location *time.Location, at time.Time) string {
	_, offsetSeconds := at.In(location).Zone()
	offsetMinutes := offsetSeconds / 60
	hours := offsetMinutes / 60
	minutes := offsetMinutes % 60
	if minutes < 0 {
		minutes = -minutes
	}
	return fmt.Sprintf("%d:%d", hours, minutes)
}
