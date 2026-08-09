package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var ErrMemoNotFound = errors.New("memo not found")

type Store struct {
	db   *sql.DB
	path string
}

type Memo struct {
	Slug        string          `json:"slug"`
	ContentHTML string          `json:"content_html,omitempty"`
	Content     string          `json:"content"`
	Tags        []string        `json:"tags"`
	CreatedAt   string          `json:"created_at"`
	CreatedUnix int64           `json:"-"`
	UpdatedAt   string          `json:"updated_at"`
	UpdatedUnix int64           `json:"-"`
	DeletedAt   string          `json:"deleted_at,omitempty"`
	MemoFrom    string          `json:"memo_from,omitempty"`
	Source      string          `json:"source,omitempty"`
	Pinned      bool            `json:"pinned"`
	Files       json.RawMessage `json:"files,omitempty"`
	Rank        float64         `json:"rank,omitempty"`
}

type SearchOptions struct {
	Query          string
	RawFTS         bool
	AnyTerm        bool
	Tags           []string
	FromUnix       int64
	ToUnix         int64
	IncludeDeleted bool
	Sort           string
	Limit          int
	Offset         int
}

type SearchResult struct {
	Memos []Memo
	Total int
}

type Stats struct {
	Memos      int       `json:"memos"`
	Deleted    int       `json:"deleted"`
	Tags       int       `json:"tags"`
	NewestAt   string    `json:"newest_at,omitempty"`
	LastSynced string    `json:"last_synced_at,omitempty"`
	TopTags    []TagStat `json:"top_tags"`
	Database   string    `json:"database"`
	DatabaseB  int64     `json:"database_bytes"`
}

type TagStat struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, path: path}
	if err := store.initialize(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize sqlite: %w", err)
		}
	}
	return s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= 1 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	schema := []string{
		`CREATE TABLE memos (
			slug TEXT PRIMARY KEY,
			content_html TEXT NOT NULL,
			content_text TEXT NOT NULL,
			tags_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT '',
			created_unix INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT '',
			updated_unix INTEGER NOT NULL DEFAULT 0,
			deleted_at TEXT NOT NULL DEFAULT '',
			memo_from TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			files_json TEXT NOT NULL DEFAULT '[]',
			synced_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_memos_created ON memos(created_unix DESC)`,
		`CREATE INDEX idx_memos_updated ON memos(updated_unix DESC)`,
		`CREATE INDEX idx_memos_deleted ON memos(deleted_at)`,
		`CREATE TABLE memo_tags (
			memo_slug TEXT NOT NULL REFERENCES memos(slug) ON DELETE CASCADE,
			tag TEXT NOT NULL,
			PRIMARY KEY (memo_slug, tag)
		)`,
		`CREATE INDEX idx_memo_tags_tag ON memo_tags(tag, memo_slug)`,
		`CREATE VIRTUAL TABLE memos_fts USING fts5(slug UNINDEXED, content, tags, tokenize='trigram')`,
		`CREATE TABLE sync_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, statement := range schema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply migration 1: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func (s *Store) UpsertMemos(ctx context.Context, memos []Memo) error {
	if len(memos) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin memo upsert: %w", err)
	}
	defer tx.Rollback()
	for _, memo := range memos {
		if memo.Slug == "" {
			return errors.New("cannot store memo without slug")
		}
		tagsJSON, err := json.Marshal(memo.Tags)
		if err != nil {
			return fmt.Errorf("encode tags for %s: %w", memo.Slug, err)
		}
		files := memo.Files
		if len(files) == 0 || string(files) == "null" {
			files = json.RawMessage("[]")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO memos(
			slug, content_html, content_text, tags_json, created_at, created_unix,
			updated_at, updated_unix, deleted_at, memo_from, source, pinned, files_json, synced_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			content_html=excluded.content_html, content_text=excluded.content_text,
			tags_json=excluded.tags_json, created_at=excluded.created_at,
			created_unix=excluded.created_unix, updated_at=excluded.updated_at,
			updated_unix=excluded.updated_unix, deleted_at=excluded.deleted_at,
			memo_from=excluded.memo_from, source=excluded.source, pinned=excluded.pinned,
			files_json=excluded.files_json, synced_at=excluded.synced_at`,
			memo.Slug, memo.ContentHTML, memo.Content, string(tagsJSON), memo.CreatedAt, memo.CreatedUnix,
			memo.UpdatedAt, memo.UpdatedUnix, memo.DeletedAt, memo.MemoFrom, memo.Source,
			boolInt(memo.Pinned), string(files), time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("upsert memo %s: %w", memo.Slug, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memo_tags WHERE memo_slug = ?`, memo.Slug); err != nil {
			return fmt.Errorf("replace tags for %s: %w", memo.Slug, err)
		}
		for _, tag := range memo.Tags {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memo_tags(memo_slug, tag) VALUES(?, ?)`, memo.Slug, tag); err != nil {
				return fmt.Errorf("insert tag for %s: %w", memo.Slug, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM memos_fts WHERE slug = ?`, memo.Slug); err != nil {
			return fmt.Errorf("refresh search index for %s: %w", memo.Slug, err)
		}
		if memo.DeletedAt == "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO memos_fts(slug, content, tags) VALUES(?, ?, ?)`, memo.Slug, memo.Content, strings.Join(memo.Tags, " ")); err != nil {
				return fmt.Errorf("index memo %s: %w", memo.Slug, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit memo upsert: %w", err)
	}
	return nil
}

func (s *Store) GetMemo(ctx context.Context, slug string, includeDeleted bool) (Memo, error) {
	query := `SELECT slug, content_html, content_text, tags_json, created_at, created_unix,
		updated_at, updated_unix, deleted_at, memo_from, source, pinned, files_json FROM memos WHERE slug = ?`
	if !includeDeleted {
		query += ` AND deleted_at = ''`
	}
	memo, err := scanMemo(s.db.QueryRowContext(ctx, query, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Memo{}, ErrMemoNotFound
	}
	return memo, err
}

func (s *Store) Search(ctx context.Context, options SearchOptions) (SearchResult, error) {
	if options.Limit <= 0 {
		options.Limit = 20
	}
	if options.Limit > 500 {
		options.Limit = 500
	}
	longTerms, shortTerms := partitionTerms(options.Query)
	useFTS := options.Query != "" && (options.RawFTS || (len(longTerms) > 0 && (!options.AnyTerm || len(shortTerms) == 0)))
	from, joins, where, args := buildSearchQuery(options, useFTS)
	countQuery := `SELECT COUNT(*) ` + from + joins + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return SearchResult{}, fmt.Errorf("count search results: %w", err)
	}
	selectRank := "0.0"
	if useFTS {
		selectRank = "bm25(memos_fts)"
	}
	query := `SELECT m.slug, m.content_html, m.content_text, m.tags_json, m.created_at, m.created_unix,
		m.updated_at, m.updated_unix, m.deleted_at, m.memo_from, m.source, m.pinned, m.files_json, ` + selectRank + ` AS rank ` + from + joins + where + searchOrder(options, useFTS) + ` LIMIT ? OFFSET ?`
	queryArgs := append(append([]any{}, args...), options.Limit, options.Offset)
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search memos: %w", err)
	}
	defer rows.Close()
	memos := make([]Memo, 0, options.Limit)
	for rows.Next() {
		memo, err := scanMemoWithRank(rows)
		if err != nil {
			return SearchResult{}, err
		}
		memos = append(memos, memo)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, fmt.Errorf("iterate search results: %w", err)
	}
	return SearchResult{Memos: memos, Total: total}, nil
}

func buildSearchQuery(options SearchOptions, useFTS bool) (string, string, string, []any) {
	from := `FROM memos m `
	joins := ""
	conditions := make([]string, 0, 8)
	args := make([]any, 0, 8)
	if useFTS {
		joins += `JOIN memos_fts ON memos_fts.slug = m.slug `
		conditions = append(conditions, `memos_fts MATCH ?`)
		query := options.Query
		if !options.RawFTS {
			longTerms, _ := partitionTerms(options.Query)
			query = safeFTSQuery(longTerms, options.AnyTerm)
		}
		args = append(args, query)
		if !options.RawFTS {
			_, shortTerms := partitionTerms(options.Query)
			for _, term := range shortTerms {
				conditions = append(conditions, `(m.content_text LIKE ? ESCAPE '\' OR m.tags_json LIKE ? ESCAPE '\')`)
				pattern := "%" + escapeLike(term) + "%"
				args = append(args, pattern, pattern)
			}
		}
	} else if options.Query != "" {
		terms := strings.Fields(options.Query)
		operator := " AND "
		if options.AnyTerm {
			operator = " OR "
		}
		likes := make([]string, 0, len(terms))
		for _, term := range terms {
			likes = append(likes, `(m.content_text LIKE ? ESCAPE '\' OR m.tags_json LIKE ? ESCAPE '\')`)
			pattern := "%" + escapeLike(term) + "%"
			args = append(args, pattern, pattern)
		}
		if len(likes) > 0 {
			conditions = append(conditions, "("+strings.Join(likes, operator)+")")
		}
	}
	if !options.IncludeDeleted {
		conditions = append(conditions, `m.deleted_at = ''`)
	}
	for _, tag := range options.Tags {
		conditions = append(conditions, `EXISTS (SELECT 1 FROM memo_tags mt WHERE mt.memo_slug = m.slug AND (mt.tag = ? OR mt.tag LIKE ? ESCAPE '\'))`)
		args = append(args, tag, escapeLike(tag)+"/%")
	}
	if options.FromUnix > 0 {
		conditions = append(conditions, `m.created_unix >= ?`)
		args = append(args, options.FromUnix)
	}
	if options.ToUnix > 0 {
		conditions = append(conditions, `m.created_unix < ?`)
		args = append(args, options.ToUnix)
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ") + " "
	}
	return from, joins, where, args
}

func searchOrder(options SearchOptions, useFTS bool) string {
	switch options.Sort {
	case "oldest":
		return `ORDER BY m.created_unix ASC, m.slug ASC`
	case "updated":
		return `ORDER BY m.updated_unix DESC, m.slug DESC`
	case "newest":
		return `ORDER BY m.created_unix DESC, m.slug DESC`
	default:
		if useFTS {
			return `ORDER BY rank ASC, m.created_unix DESC`
		}
		return `ORDER BY m.created_unix DESC, m.slug DESC`
	}
}

func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_state(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set sync state %s: %w", key, err)
	}
	return nil
}

func (s *Store) State(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read sync state %s: %w", key, err)
	}
	return value, nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{Database: s.path, TopTags: []TagStat{}}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE deleted_at = ''), COUNT(*) FILTER (WHERE deleted_at != ''), COALESCE(MAX(created_at), '') FROM memos`).Scan(&stats.Memos, &stats.Deleted, &stats.NewestAt); err != nil {
		return Stats{}, fmt.Errorf("read memo stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT tag) FROM memo_tags`).Scan(&stats.Tags); err != nil {
		return Stats{}, fmt.Errorf("read tag stats: %w", err)
	}
	stats.LastSynced, _ = s.State(ctx, "last_synced_at")
	rows, err := s.db.QueryContext(ctx, `SELECT mt.tag, COUNT(*) FROM memo_tags mt JOIN memos m ON m.slug=mt.memo_slug WHERE m.deleted_at='' GROUP BY mt.tag ORDER BY COUNT(*) DESC, mt.tag LIMIT 10`)
	if err != nil {
		return Stats{}, fmt.Errorf("read top tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tag TagStat
		if err := rows.Scan(&tag.Tag, &tag.Count); err != nil {
			return Stats{}, fmt.Errorf("scan tag stats: %w", err)
		}
		stats.TopTags = append(stats.TopTags, tag)
	}
	if fileInfo, err := os.Stat(s.path); err == nil {
		stats.DatabaseB = fileInfo.Size()
	}
	return stats, nil
}

type scanner interface{ Scan(...any) error }

func scanMemo(row scanner) (Memo, error) {
	var memo Memo
	var tagsJSON, filesJSON string
	var pinned int
	err := row.Scan(&memo.Slug, &memo.ContentHTML, &memo.Content, &tagsJSON, &memo.CreatedAt, &memo.CreatedUnix,
		&memo.UpdatedAt, &memo.UpdatedUnix, &memo.DeletedAt, &memo.MemoFrom, &memo.Source, &pinned, &filesJSON)
	if err != nil {
		return Memo{}, err
	}
	memo.Pinned = pinned != 0
	_ = json.Unmarshal([]byte(tagsJSON), &memo.Tags)
	memo.Files = json.RawMessage(filesJSON)
	return memo, nil
}

func scanMemoWithRank(row scanner) (Memo, error) {
	var memo Memo
	var tagsJSON, filesJSON string
	var pinned int
	err := row.Scan(&memo.Slug, &memo.ContentHTML, &memo.Content, &tagsJSON, &memo.CreatedAt, &memo.CreatedUnix,
		&memo.UpdatedAt, &memo.UpdatedUnix, &memo.DeletedAt, &memo.MemoFrom, &memo.Source, &pinned, &filesJSON, &memo.Rank)
	if err != nil {
		return Memo{}, fmt.Errorf("scan memo: %w", err)
	}
	memo.Pinned = pinned != 0
	_ = json.Unmarshal([]byte(tagsJSON), &memo.Tags)
	memo.Files = json.RawMessage(filesJSON)
	return memo, nil
}

func safeFTSQuery(terms []string, any bool) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	join := " AND "
	if any {
		join = " OR "
	}
	return strings.Join(quoted, join)
}

func partitionTerms(query string) (long, short []string) {
	for _, term := range strings.Fields(query) {
		if utf8.RuneCountInString(term) >= 3 {
			long = append(long, term)
		} else {
			short = append(short, term)
		}
	}
	return long, short
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
