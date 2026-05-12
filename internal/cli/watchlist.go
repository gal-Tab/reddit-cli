package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"reddit-pp-cli/internal/store"
)

func newWatchlistCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watchlist",
		Short: "Bookmark subs and users; refresh them all and diff what's new since the last sync.",
		Long: strings.Trim(`
The watchlist is a tiny local table of subreddits and users you care
about. It replaces the open-12-tabs-and-Ctrl-F ritual: add entries
once, then "watchlist refresh" pulls them all and "watchlist diff"
tells you what's new since last sync.

Commands:
  add <target> [--user]   Bookmark a subreddit (default) or a user
  remove <target>         Drop an entry
  list                    Show all entries
  refresh                 Re-sync every entry; updates last_synced_at
  diff                    What's new since the last refresh, grouped by entry
`, "\n"),
	}
	cmd.AddCommand(newWatchlistAddCmd(flags))
	cmd.AddCommand(newWatchlistRemoveCmd(flags))
	cmd.AddCommand(newWatchlistListCmd(flags))
	cmd.AddCommand(newWatchlistRefreshCmd(flags))
	cmd.AddCommand(newWatchlistDiffCmd(flags))
	return cmd
}

func newWatchlistAddCmd(flags *rootFlags) *cobra.Command {
	var asUser bool
	var dbPath string
	cmd := &cobra.Command{
		Use:     "add <target>",
		Short:   "Bookmark a subreddit (default) or a user (--user)",
		Example: "  reddit-pp-cli watchlist add golang\n  reddit-pp-cli watchlist add spez --user",
		Annotations: map[string]string{"mcp:read-only": "false"}, // mutates local state
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			target := normalizeTarget(args[0], asUser)
			kind := "subreddit"
			if asUser {
				kind = "user"
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would add watchlist entry %s/%s\n", kind, target)
				return nil
			}
			db, err := openWatchlistDB(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			added, err := store.WatchlistAdd(cmd.Context(), db.DB(), kind, target)
			if err != nil {
				return err
			}
			result := map[string]any{"kind": kind, "target": target, "added": added}
			return printJSONFiltered(cmd.OutOrStdout(), result, flags)
		},
	}
	cmd.Flags().BoolVar(&asUser, "user", false, "Treat target as a username (default: subreddit)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

func newWatchlistRemoveCmd(flags *rootFlags) *cobra.Command {
	var asUser bool
	var dbPath string
	cmd := &cobra.Command{
		Use:     "remove <target>",
		Short:   "Remove a subreddit (default) or a user (--user) from the watchlist",
		Example: "  reddit-pp-cli watchlist remove golang",
		Annotations: map[string]string{"mcp:read-only": "false"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			target := normalizeTarget(args[0], asUser)
			kind := "subreddit"
			if asUser {
				kind = "user"
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would remove watchlist entry %s/%s\n", kind, target)
				return nil
			}
			db, err := openWatchlistDB(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			removed, err := store.WatchlistRemove(cmd.Context(), db.DB(), kind, target)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), map[string]any{"kind": kind, "target": target, "removed": removed}, flags)
		},
	}
	cmd.Flags().BoolVar(&asUser, "user", false, "Treat target as a username")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

func newWatchlistListCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "Show all watchlist entries with their last_synced_at",
		Example: "  reddit-pp-cli watchlist list --json",
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			db, err := openWatchlistDB(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			entries, err := store.WatchlistList(cmd.Context(), db.DB())
			if err != nil {
				return err
			}
			if entries == nil {
				entries = []store.WatchlistEntry{}
			}
			return printJSONFiltered(cmd.OutOrStdout(), entries, flags)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

func newWatchlistRefreshCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	var sort string
	var limit int
	cmd := &cobra.Command{
		Use:     "refresh",
		Short:   "Re-sync every watchlist entry (calls /r/<sub>/<sort>.json or /user/<u>/overview.json) and stamps last_synced_at",
		Example: "  reddit-pp-cli watchlist refresh\n  reddit-pp-cli watchlist refresh --sort top --limit 100",
		Annotations: map[string]string{"mcp:read-only": "false"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				fmt.Fprintln(cmd.OutOrStdout(), "would refresh all watchlist entries")
				return nil
			}
			db, err := openWatchlistDB(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			entries, err := store.WatchlistList(cmd.Context(), db.DB())
			if err != nil {
				return err
			}
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			type refreshRow struct {
				Kind    string `json:"kind"`
				Target  string `json:"target"`
				Fetched int    `json:"fetched"`
				Error   string `json:"error,omitempty"`
			}
			results := make([]refreshRow, 0, len(entries))
			for _, e := range entries {
				row := refreshRow{Kind: e.Kind, Target: e.Target}
				var path string
				params := map[string]string{"limit": fmt.Sprintf("%d", limit)}
				switch e.Kind {
				case "subreddit":
					path = "/r/" + e.Target + "/" + sort + ".json"
				case "user":
					path = "/user/" + e.Target + "/overview.json"
				}
				posts, err := fetchAndPersist(cmd.Context(), c, db, path, params)
				if err != nil {
					row.Error = err.Error()
					results = append(results, row)
					continue
				}
				row.Fetched = len(posts)
				_ = store.WatchlistMarkSynced(cmd.Context(), db.DB(), e.Kind, e.Target, len(posts))
				results = append(results, row)
			}
			return printJSONFiltered(cmd.OutOrStdout(), results, flags)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	cmd.Flags().StringVar(&sort, "sort", "hot", "Subreddit sort: hot, new, top, rising, controversial")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max items per entry per refresh")
	return cmd
}

func newWatchlistDiffCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:     "diff",
		Short:   "Posts created since each entry's last_synced_at, grouped by entry",
		Example: "  reddit-pp-cli watchlist diff --json",
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return nil
			}
			db, err := openWatchlistDB(cmd.Context(), dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			entries, err := store.WatchlistList(cmd.Context(), db.DB())
			if err != nil {
				return err
			}
			out, err := computeWatchlistDiff(cmd.Context(), db.DB(), entries)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), out, flags)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

type watchlistDiffEntry struct {
	Kind         string             `json:"kind"`
	Target       string             `json:"target"`
	LastSyncedAt *time.Time         `json:"last_synced_at,omitempty"`
	NewPostCount int                `json:"new_post_count"`
	NewPosts     []watchlistDiffRow `json:"new_posts,omitempty"`
}

type watchlistDiffRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Subreddit   string `json:"subreddit"`
	Author      string `json:"author"`
	Permalink   string `json:"permalink"`
	Score       int    `json:"score"`
	NumComments int    `json:"num_comments"`
}

func computeWatchlistDiff(ctx context.Context, db *sql.DB, entries []store.WatchlistEntry) ([]watchlistDiffEntry, error) {
	out := make([]watchlistDiffEntry, 0, len(entries))
	for _, e := range entries {
		row := watchlistDiffEntry{Kind: e.Kind, Target: e.Target, LastSyncedAt: e.LastSyncedAt}
		if e.LastSyncedAt == nil {
			out = append(out, row)
			continue
		}
		// Pull posts in this sub/user created after last_synced_at.
		var rows *sql.Rows
		var err error
		switch e.Kind {
		case "subreddit":
			rows, err = db.QueryContext(ctx,
				`SELECT data FROM resources WHERE resource_type='post'
				    AND lower(json_extract(data, '$.subreddit')) = lower(?)
				    AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >= CAST(strftime('%s', ?) AS INTEGER)
				  ORDER BY CAST(json_extract(data, '$.created_utc') AS INTEGER) DESC`,
				e.Target, e.LastSyncedAt.UTC().Format(time.RFC3339))
		case "user":
			rows, err = db.QueryContext(ctx,
				`SELECT data FROM resources WHERE resource_type='post'
				    AND lower(json_extract(data, '$.author')) = lower(?)
				    AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >= CAST(strftime('%s', ?) AS INTEGER)
				  ORDER BY CAST(json_extract(data, '$.created_utc') AS INTEGER) DESC`,
				e.Target, e.LastSyncedAt.UTC().Format(time.RFC3339))
		default:
			out = append(out, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("diff query for %s/%s: %w", e.Kind, e.Target, err)
		}
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				rows.Close()
				return nil, err
			}
			var p redditPost
			if err := json.Unmarshal([]byte(data), &p); err != nil || p.ID == "" {
				continue
			}
			row.NewPosts = append(row.NewPosts, watchlistDiffRow{
				ID: p.ID, Title: p.Title, Subreddit: p.Subreddit, Author: p.Author,
				Permalink: "https://www.reddit.com" + p.Permalink,
				Score:     p.Score, NumComments: p.NumComments,
			})
		}
		rows.Close()
		row.NewPostCount = len(row.NewPosts)
		out = append(out, row)
	}
	return out, nil
}

// openWatchlistDB opens the store and ensures the extras tables. Used by
// every watchlist subcommand.
func openWatchlistDB(ctx context.Context, dbPath string) (*store.Store, error) {
	if dbPath == "" {
		dbPath = defaultDBPath("reddit-pp-cli")
	}
	db, err := store.OpenWithContext(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := store.EnsureRedditExtras(ctx, db.DB()); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func normalizeTarget(s string, asUser bool) string {
	s = strings.TrimSpace(s)
	if asUser {
		s = strings.TrimPrefix(s, "u/")
		s = strings.TrimPrefix(s, "/u/")
	} else {
		s = strings.TrimPrefix(s, "r/")
		s = strings.TrimPrefix(s, "/r/")
	}
	return s
}
