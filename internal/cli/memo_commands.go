package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/songquanpeng/flomo-cli/internal/service"
	"github.com/songquanpeng/flomo-cli/internal/storage"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSyncCommand(rt *runtime) *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Incrementally mirror remote memos into local SQLite",
		Args:  cobra.NoArgs,
		Long: `Incrementally mirror remote memos, edits, and deletions into local SQLite.

Sync is resumable and uses a server cursor. Use --full to start reading from the beginning;
existing local rows are safely upserted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rt.validateFormat(); err != nil {
				return err
			}
			client, _, err := rt.authenticatedClient()
			if err != nil {
				return err
			}
			store, _, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := service.NewMemoService(client, store, rt.timezone).Sync(commandContext(cmd), full)
			if err != nil {
				return err
			}
			if rt.wantsJSON() {
				response := struct {
					service.SyncResult
					SuggestedCommands []string `json:"suggested_commands"`
				}{
					SyncResult:        result,
					SuggestedCommands: []string{"flomo list --json", "flomo search '<topic>' --json"},
				}
				return writeJSON(rt.out, response, true)
			}
			fmt.Fprintf(rt.out, "Synced %d memo changes in %d page(s). Local index is current as of %s.\n", result.Fetched, result.Pages, result.SyncedAt)
			fmt.Fprintln(rt.out, "Next: flomo list --json, or flomo search '<topic>' --json")
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "read all remote memo history instead of resuming the cursor")
	return cmd
}

func newAddCommand(rt *runtime) *cobra.Command {
	var tags []string
	var createdAt string
	var contentHTML bool
	cmd := &cobra.Command{
		Use:   "add [text|-]",
		Short: "Add a memo remotely and cache it locally",
		Args:  cobra.ArbitraryArgs,
		Long: `Add a memo to flomo, then immediately place the returned memo in local SQLite.

With no text argument or with "-", content is read from stdin. Plain text is HTML-escaped;
use --html only when deliberately providing flomo-compatible HTML.`,
		Example: `  flomo add "一个新想法" --tag 灵感 --tag 工作/项目
  printf 'multi-line\nmemo' | flomo add - --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rt.validateFormat(); err != nil {
				return err
			}
			content, err := memoInput(args)
			if err != nil {
				return err
			}
			created, err := parseDateTime(createdAt, rt.timezone, time.Now())
			if err != nil {
				return fmt.Errorf("invalid --created-at: %w", err)
			}
			client, _, err := rt.authenticatedClient()
			if err != nil {
				return err
			}
			store, _, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			memo, err := service.NewMemoService(client, store, rt.timezone).Add(commandContext(cmd), content, tags, created, contentHTML)
			if err != nil {
				return fmt.Errorf("add memo: %w", err)
			}
			if rt.wantsJSON() {
				memo.ContentHTML = ""
				response := struct {
					storage.Memo
					SuggestedCommands []string `json:"suggested_commands"`
				}{
					Memo:              memo,
					SuggestedCommands: []string{"flomo get " + memo.Slug + " --json"},
				}
				return writeJSON(rt.out, response, true)
			}
			fmt.Fprintf(rt.out, "Added %s\nInspect: flomo get %s --json\n", memo.Slug, memo.Slug)
			return nil
		},
	}
	cmd.Flags().StringSliceVarP(&tags, "tag", "t", nil, "append a tag (repeatable; hierarchical tags use /)")
	cmd.Flags().StringVar(&createdAt, "created-at", "", "creation time: RFC3339, 'YYYY-MM-DD HH:MM:SS', or YYYY-MM-DD")
	cmd.Flags().BoolVar(&contentHTML, "html", false, "treat input as flomo-compatible HTML")
	return cmd
}

func memoInput(args []string) (string, error) {
	if len(args) > 0 && !(len(args) == 1 && args[0] == "-") {
		return strings.Join(args, " "), nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("provide memo text as an argument, or pipe content on stdin")
	}
	content, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read memo from stdin: %w", err)
	}
	return string(content), nil
}

type searchFlags struct {
	tags           []string
	from           string
	to             string
	sort           string
	limit          int
	offset         int
	any            bool
	rawFTS         bool
	includeDeleted bool
	includeHTML    bool
	output         string
	maxChars       int
}

type searchResponse struct {
	Query   string          `json:"query"`
	Total   int             `json:"total"`
	Count   int             `json:"count"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
	HasMore bool            `json:"has_more"`
	Next    *paginationHint `json:"next,omitempty"`
	Items   []storage.Memo  `json:"items"`
}

type paginationHint struct {
	Offset  int    `json:"offset"`
	Command string `json:"command"`
}

func newSearchCommand(rt *runtime) *cobra.Command {
	flags := searchFlags{limit: 20, output: "text"}
	cmd := &cobra.Command{
		Use:     "search <query>",
		Aliases: []string{"find"},
		Short:   "Search the local memo index (offline)",
		Args:    cobra.ExactArgs(1),
		Long: `Search local memo text and tags without contacting flomo.

Space-separated terms use AND by default. Trigram full-text search handles Chinese and
substring queries; one- or two-character terms transparently use literal matching.
Results are ranked by relevance unless --sort is specified.`,
		Example: `  flomo search "LLM 写作" --json
  flomo search "项目复盘" --tag 工作 --from 2025-01-01 --output jsonl
  flomo search "人工智能 AI" --any --limit 50`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, rt, args[0], flags)
		},
	}
	bindSearchFlags(cmd, &flags)
	return cmd
}

func newListCommand(rt *runtime) *cobra.Command {
	flags := searchFlags{limit: 20, output: "text", sort: "newest"}
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "recent"},
		Short:   "List local memos, newest first",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, rt, "", flags)
		},
	}
	bindSearchFlags(cmd, &flags)
	cmd.Flags().Lookup("raw-fts").Hidden = true
	cmd.Flags().Lookup("any").Hidden = true
	return cmd
}

func bindSearchFlags(cmd *cobra.Command, flags *searchFlags) {
	cmd.Flags().StringSliceVarP(&flags.tags, "tag", "t", nil, "require exact tag or a child of it (repeatable, ANDed)")
	cmd.Flags().StringVar(&flags.from, "from", "", "include memos on/after this date or RFC3339 timestamp")
	cmd.Flags().StringVar(&flags.to, "to", "", "include memos on/before this date or before this RFC3339 timestamp")
	cmd.Flags().StringVar(&flags.sort, "sort", "", "sort: relevance, newest, oldest, or updated")
	cmd.Flags().IntVarP(&flags.limit, "limit", "n", flags.limit, "maximum results (1-500)")
	cmd.Flags().IntVar(&flags.offset, "offset", 0, "skip this many results for pagination")
	cmd.Flags().BoolVar(&flags.any, "any", false, "match any term instead of requiring all terms")
	cmd.Flags().BoolVar(&flags.rawFTS, "raw-fts", false, "interpret query as raw SQLite FTS5 syntax")
	cmd.Flags().BoolVar(&flags.includeDeleted, "include-deleted", false, "include remotely deleted memos")
	cmd.Flags().BoolVar(&flags.includeHTML, "include-html", false, "include original content_html in structured output")
	cmd.Flags().StringVarP(&flags.output, "output", "o", flags.output, "result format: text, json, jsonl, or markdown")
	cmd.Flags().IntVar(&flags.maxChars, "max-chars", 0, "truncate each memo's text to this many characters (0 = unlimited)")
}

func runSearch(cmd *cobra.Command, rt *runtime, query string, flags searchFlags) error {
	if rt.wantsJSON() {
		flags.output = "json"
	}
	if flags.limit < 1 || flags.limit > 500 {
		return errors.New("--limit must be between 1 and 500")
	}
	if flags.offset < 0 {
		return errors.New("--offset cannot be negative")
	}
	if flags.sort != "" && flags.sort != "relevance" && flags.sort != "newest" && flags.sort != "oldest" && flags.sort != "updated" {
		return fmt.Errorf("unsupported --sort %q", flags.sort)
	}
	if query == "" && flags.sort == "relevance" {
		flags.sort = "newest"
	}
	fromUnix, toUnix, err := parseRange(flags.from, flags.to, rt.timezone)
	if err != nil {
		return err
	}
	store, _, err := rt.openStore()
	if err != nil {
		return err
	}
	defer store.Close()
	result, err := store.Search(commandContext(cmd), storage.SearchOptions{
		Query: query, RawFTS: flags.rawFTS, AnyTerm: flags.any, Tags: cleanTags(flags.tags),
		FromUnix: fromUnix, ToUnix: toUnix, IncludeDeleted: flags.includeDeleted,
		Sort: flags.sort, Limit: flags.limit, Offset: flags.offset,
	})
	if err != nil {
		if flags.rawFTS {
			return fmt.Errorf("raw FTS query failed: %w", err)
		}
		return err
	}
	items := stripHTMLField(result.Memos, flags.includeHTML, flags.maxChars)
	response := searchResponse{Query: query, Total: result.Total, Count: len(items), Limit: flags.limit, Offset: flags.offset, HasMore: flags.offset+len(items) < result.Total, Items: items}
	if response.HasMore {
		nextOffset := flags.offset + len(items)
		commandName := "search"
		if query == "" {
			commandName = "list"
		}
		response.Next = &paginationHint{Offset: nextOffset, Command: buildNextPageCommand(commandName, query, flags, nextOffset)}
	}
	switch flags.output {
	case "json":
		return writeJSON(rt.out, response, true)
	case "jsonl":
		encoder := json.NewEncoder(rt.out)
		encoder.SetEscapeHTML(false)
		for _, memo := range items {
			if err := encoder.Encode(memo); err != nil {
				return err
			}
		}
		writePaginationHint(rt.errOut, response)
		return nil
	case "markdown":
		for _, memo := range items {
			fmt.Fprintf(rt.out, "## %s — %s\n\n%s\n\n", memo.Slug, memo.CreatedAt, memo.Content)
		}
		writePaginationHint(rt.errOut, response)
		return nil
	case "text":
		if len(items) == 0 {
			if result.Total > 0 {
				fmt.Fprintf(rt.out, "No results at offset %d; %d result(s) exist. Restart with --offset 0.\n", flags.offset, result.Total)
				return nil
			}
			fmt.Fprintln(rt.out, "No local memos matched. Run `flomo sync` if the local index may be stale.")
			return nil
		}
		for index, memo := range items {
			if index > 0 {
				fmt.Fprintln(rt.out, "---")
			}
			writeMemoText(rt.out, memo, flags.maxChars)
		}
		fmt.Fprintf(rt.errOut, "\n%d of %d result(s); offset %d\n", len(items), result.Total, flags.offset)
		writePaginationHint(rt.errOut, response)
		return nil
	default:
		return fmt.Errorf("unsupported --output %q; use text, json, jsonl, or markdown", flags.output)
	}
}

func buildNextPageCommand(commandName, query string, flags searchFlags, nextOffset int) string {
	parts := []string{"flomo", commandName}
	if query != "" {
		parts = append(parts, shellQuote(query))
	}
	for _, tag := range cleanTags(flags.tags) {
		parts = append(parts, "--tag", shellQuote(tag))
	}
	if flags.from != "" {
		parts = append(parts, "--from", shellQuote(flags.from))
	}
	if flags.to != "" {
		parts = append(parts, "--to", shellQuote(flags.to))
	}
	if flags.sort != "" && flags.sort != "relevance" && !(commandName == "list" && flags.sort == "newest") {
		parts = append(parts, "--sort", flags.sort)
	}
	if flags.any {
		parts = append(parts, "--any")
	}
	if flags.rawFTS {
		parts = append(parts, "--raw-fts")
	}
	if flags.includeDeleted {
		parts = append(parts, "--include-deleted")
	}
	if flags.includeHTML {
		parts = append(parts, "--include-html")
	}
	if flags.maxChars > 0 {
		parts = append(parts, "--max-chars", strconv.Itoa(flags.maxChars))
	}
	if flags.output != "text" {
		parts = append(parts, "--output", flags.output)
	}
	parts = append(parts, "--limit", strconv.Itoa(flags.limit), "--offset", strconv.Itoa(nextOffset))
	return strings.Join(parts, " ")
}

func writePaginationHint(writer io.Writer, response searchResponse) {
	if response.Next != nil {
		fmt.Fprintf(writer, "More results are available. Next page:\n  %s\n", response.Next.Command)
		return
	}
	if response.Total > 0 {
		fmt.Fprintln(writer, "End of results; no additional page is available.")
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func newGetCommand(rt *runtime) *cobra.Command {
	var includeDeleted, includeHTML bool
	cmd := &cobra.Command{
		Use:   "get <slug>",
		Short: "Get one local memo by its stable slug",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			memo, err := store.GetMemo(commandContext(cmd), args[0], includeDeleted)
			if err != nil {
				return err
			}
			if rt.wantsJSON() {
				if !includeHTML {
					memo.ContentHTML = ""
				}
				return writeJSON(rt.out, memo, true)
			}
			writeMemoText(rt.out, memo, 0)
			return nil
		},
	}
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "allow retrieving a deleted memo")
	cmd.Flags().BoolVar(&includeHTML, "include-html", false, "include original content_html in JSON")
	return cmd
}

func newStatsCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Summarize the local memo index and most-used tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			stats, err := store.Stats(commandContext(cmd))
			if err != nil {
				return err
			}
			if rt.wantsJSON() {
				return writeJSON(rt.out, stats, true)
			}
			fmt.Fprintf(rt.out, "Memos: %d active, %d deleted\nTags: %d\nLast sync: %s\nDatabase: %s (%d bytes)\n", stats.Memos, stats.Deleted, stats.Tags, emptyDash(stats.LastSynced), stats.Database, stats.DatabaseB)
			if len(stats.TopTags) > 0 {
				fmt.Fprintln(rt.out, "Top tags:")
				for _, tag := range stats.TopTags {
					fmt.Fprintf(rt.out, "  %-24s %d\n", "#"+tag.Tag, tag.Count)
				}
			}
			return nil
		},
	}
}

func newExportCommand(rt *runtime) *cobra.Command {
	var output string
	var includeDeleted, includeHTML bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the complete local corpus for an agent or backup",
		Args:  cobra.NoArgs,
		Long: `Export all local memos in deterministic newest-first order.

JSONL is the default because agents can stream it without loading the entire corpus.
This command is offline and writes only to stdout; redirect it to a file if desired.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rt.wantsJSON() {
				output = "json"
			}
			if output != "jsonl" && output != "json" && output != "markdown" {
				return fmt.Errorf("unsupported --output %q; use jsonl, json, or markdown", output)
			}
			store, _, err := rt.openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			all := make([]storage.Memo, 0)
			for offset := 0; ; offset += 500 {
				result, err := store.Search(commandContext(cmd), storage.SearchOptions{IncludeDeleted: includeDeleted, Sort: "newest", Limit: 500, Offset: offset})
				if err != nil {
					return err
				}
				items := stripHTMLField(result.Memos, includeHTML, 0)
				if output == "jsonl" {
					for _, memo := range items {
						if err := writeJSON(rt.out, memo, false); err != nil {
							return err
						}
					}
				} else if output == "markdown" {
					for _, memo := range items {
						fmt.Fprintf(rt.out, "## %s — %s\n\n%s\n\n", memo.Slug, memo.CreatedAt, memo.Content)
					}
				} else {
					all = append(all, items...)
				}
				if offset+len(items) >= result.Total {
					break
				}
			}
			if output == "json" {
				return writeJSON(rt.out, all, true)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "jsonl", "export format: jsonl, json, or markdown")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include remotely deleted memos")
	cmd.Flags().BoolVar(&includeHTML, "include-html", false, "include original content_html")
	return cmd
}

func newAgentGuideCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "agent-guide",
		Short: "Print concise operating guidance for an AI agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(rt.out, `Agent workflow for flomo-cli

1. Freshness: run "flomo sync --json" before work that requires current remote notes.
2. Discovery: use "flomo search <topic> --json --limit 50"; when has_more is true, run next.command verbatim.
3. Narrowing: repeat --tag for AND filters; use --from/--to; use --any for broad recall.
4. Retrieval: use "flomo get <slug> --json" for a stable exact record.
5. Large corpus: use "flomo export --output jsonl" and process one JSON object per line.
6. Writing: pipe content into "flomo add - --tag <tag> --json". Never place passwords in argv; use login --password-stdin.

Search is local/offline. JSON fields are stable: query, total, count, limit, offset,
has_more, and items[]. Each item contains slug, plain content, tags, timestamps,
source metadata, pin state, and attachments metadata.
`)
			return nil
		},
	}
}

func cleanTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.TrimPrefix(tag, "#"))
		if tag != "" {
			result = append(result, tag)
		}
	}
	return result
}

func parseRange(from, to, timezone string) (int64, int64, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return 0, 0, fmt.Errorf("load timezone %q: %w", timezone, err)
	}
	var fromUnix, toUnix int64
	if from != "" {
		parsed, dateOnly, err := parseUserTime(from, location)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid --from: %w", err)
		}
		_ = dateOnly
		fromUnix = parsed.Unix()
	}
	if to != "" {
		parsed, dateOnly, err := parseUserTime(to, location)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid --to: %w", err)
		}
		if dateOnly {
			parsed = parsed.AddDate(0, 0, 1)
		}
		toUnix = parsed.Unix()
	}
	if fromUnix > 0 && toUnix > 0 && fromUnix >= toUnix {
		return 0, 0, errors.New("--from must be before --to")
	}
	return fromUnix, toUnix, nil
}

func parseDateTime(value, timezone string, fallback time.Time) (time.Time, error) {
	if value == "" {
		return fallback, nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	parsed, _, err := parseUserTime(value, location)
	return parsed, err
}

func parseUserTime(value string, location *time.Location) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, false, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, location); err == nil {
		return parsed, false, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		return parsed, true, nil
	}
	return time.Time{}, false, errors.New("expected RFC3339, 'YYYY-MM-DD HH:MM:SS', or YYYY-MM-DD")
}
