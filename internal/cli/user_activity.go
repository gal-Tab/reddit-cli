package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gal-Tab/reddit-cli/internal/store"
)

// newUserActivityCmd is registered as a child of the existing `user`
// command (added via root.go wiring after the generator's user cmd is
// constructed). Calls /user/<name>/overview.json with cursor walking,
// then aggregates locally.
func newUserActivityCmd(flags *rootFlags) *cobra.Command {
	var sinceStr string
	var groupBy string
	var maxPages int
	var dbPath string

	cmd := &cobra.Command{
		Use:   "activity <username>",
		Short: "All posts and comments by a user in a window, grouped by subreddit with karma sums.",
		Long: strings.Trim(`
Walks /user/<name>/overview.json (cursor-based) until items predate
the --since cutoff, persists everything to the local store, and groups
by subreddit with counts and score sums per sub.

Reddit caps overview at ~1000 items, so very prolific users may have
older history clipped — the report will warn when the cursor walk
stopped due to the cap rather than reaching the cutoff.
`, "\n"),
		Example: strings.Trim(`
  reddit-cli user activity spez --since 30d --group-by sub --json
  reddit-cli user activity gallowboob --since 7d --group-by sub
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			user := strings.TrimPrefix(strings.TrimPrefix(args[0], "u/"), "/u/")
			since, err := parseHumanDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would aggregate user %s activity since=%s group-by=%s\n", user, since, groupBy)
				return nil
			}
			if dbPath == "" {
				dbPath = defaultDBPath("reddit-cli")
			}
			db, err := store.OpenWithContext(cmd.Context(), dbPath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer db.Close()
			if err := store.EnsureRedditExtras(cmd.Context(), db.DB()); err != nil {
				return err
			}
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			cutoff := time.Now().Add(-since).Unix()
			pagesWalked, hitCap, err := walkUserOverview(cmd.Context(), c, db, user, cutoff, maxPages)
			if err != nil {
				return classifyAPIError(err, flags)
			}
			report, err := aggregateUserActivity(cmd.Context(), db.DB(), user, cutoff, groupBy)
			if err != nil {
				return err
			}
			report.PagesWalked = pagesWalked
			report.HitPageCap = hitCap
			return printJSONFiltered(cmd.OutOrStdout(), report, flags)
		},
	}
	cmd.Flags().StringVar(&sinceStr, "since", "30d", "Time window (e.g. 7d, 30d, 90d)")
	cmd.Flags().StringVar(&groupBy, "group-by", "sub", "Aggregation: sub | kind")
	cmd.Flags().IntVar(&maxPages, "max-pages", 10, "Cap on overview pagination walks")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

// walkUserOverview pages through /user/<u>/overview.json until items
// predate `cutoffUnix` or the page cap is hit. Returns pagesWalked and
// hitCap (true if we stopped at the cap rather than reaching cutoff).
func walkUserOverview(ctx context.Context, c apiClient, db *store.Store, user string, cutoffUnix int64, maxPages int) (int, bool, error) {
	if maxPages <= 0 {
		maxPages = 10
	}
	after := ""
	for page := 0; page < maxPages; page++ {
		params := map[string]string{"limit": "100"}
		if after != "" {
			params["after"] = after
		}
		raw, err := c.Get("/user/"+user+"/overview.json", params)
		if err != nil {
			return page, false, err
		}
		children, nextAfter, err := parseListing(raw)
		if err != nil {
			return page, false, err
		}
		if len(children) == 0 {
			return page + 1, false, nil
		}
		// Posts go to the post resource; comments to the comment
		// resource. Reddit overview interleaves both kinds.
		var postChildren, commentChildren []json.RawMessage
		var oldestUTC int64 = 1<<62 - 1
		for _, c := range children {
			var probe struct {
				CreatedUTC float64 `json:"created_utc"`
				Subreddit  string  `json:"subreddit"`
				ParentID   string  `json:"parent_id"` // present on comments
				Body       string  `json:"body"`
			}
			if err := json.Unmarshal(c, &probe); err != nil {
				continue
			}
			if int64(probe.CreatedUTC) < oldestUTC {
				oldestUTC = int64(probe.CreatedUTC)
			}
			if probe.Body != "" || probe.ParentID != "" {
				commentChildren = append(commentChildren, c)
			} else {
				postChildren = append(postChildren, c)
			}
		}
		if len(postChildren) > 0 {
			posts := extractPosts(postChildren)
			if _, err := persistPosts(ctx, db, posts, postChildren); err != nil {
				return page, false, err
			}
		}
		if len(commentChildren) > 0 {
			if _, _, err := db.UpsertBatch("comment", commentChildren); err != nil {
				return page, false, err
			}
		}
		// Stop if cursor exhausted or oldest item predates cutoff.
		if nextAfter == "" || oldestUTC < cutoffUnix {
			return page + 1, false, nil
		}
		after = nextAfter
	}
	return maxPages, true, nil
}

// userActivityReport is the aggregated output.
type userActivityReport struct {
	User        string         `json:"user"`
	WindowSince string         `json:"window_since"`
	GroupBy     string         `json:"group_by"`
	Groups      []userActivityRow `json:"groups"`
	PagesWalked int            `json:"pages_walked"`
	HitPageCap  bool           `json:"hit_page_cap"`
}

type userActivityRow struct {
	Group        string  `json:"group"`
	PostCount    int     `json:"post_count"`
	CommentCount int     `json:"comment_count"`
	ScoreSum     int     `json:"score_sum"`
	FirstAt      *string `json:"first_at,omitempty"`
	LastAt       *string `json:"last_at,omitempty"`
}

func aggregateUserActivity(ctx context.Context, db *sql.DB, user string, cutoffUnix int64, groupBy string) (userActivityReport, error) {
	report := userActivityReport{
		User:        user,
		WindowSince: time.Unix(cutoffUnix, 0).UTC().Format(time.RFC3339),
		GroupBy:     groupBy,
	}
	groupExpr := "json_extract(data, '$.subreddit')"
	if groupBy == "kind" {
		groupExpr = "resource_type"
	}
	q := fmt.Sprintf(`
		SELECT %s AS group_key,
		       resource_type,
		       COUNT(*) AS cnt,
		       SUM(CAST(json_extract(data, '$.score') AS INTEGER)) AS score_sum,
		       MIN(CAST(json_extract(data, '$.created_utc') AS INTEGER)) AS first_at,
		       MAX(CAST(json_extract(data, '$.created_utc') AS INTEGER)) AS last_at
		  FROM resources
		 WHERE resource_type IN ('post','comment')
		   AND lower(json_extract(data, '$.author')) = lower(?)
		   AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >= ?
		 GROUP BY group_key, resource_type`, groupExpr)
	rows, err := db.QueryContext(ctx, q, user, cutoffUnix)
	if err != nil {
		return report, fmt.Errorf("aggregate query: %w", err)
	}
	defer rows.Close()
	idx := map[string]*userActivityRow{}
	for rows.Next() {
		var groupKey, resType string
		var cnt, scoreSum, firstAt, lastAt sql.NullInt64
		var groupKeyN sql.NullString
		if err := rows.Scan(&groupKeyN, &resType, &cnt, &scoreSum, &firstAt, &lastAt); err != nil {
			return report, err
		}
		groupKey = groupKeyN.String
		if groupKey == "" {
			groupKey = "(unknown)"
		}
		row, ok := idx[groupKey]
		if !ok {
			row = &userActivityRow{Group: groupKey}
			idx[groupKey] = row
		}
		switch resType {
		case "post":
			row.PostCount = int(cnt.Int64)
		case "comment":
			row.CommentCount = int(cnt.Int64)
		}
		row.ScoreSum += int(scoreSum.Int64)
		if firstAt.Valid {
			t := time.Unix(firstAt.Int64, 0).UTC().Format(time.RFC3339)
			if row.FirstAt == nil || *row.FirstAt > t {
				row.FirstAt = &t
			}
		}
		if lastAt.Valid {
			t := time.Unix(lastAt.Int64, 0).UTC().Format(time.RFC3339)
			if row.LastAt == nil || *row.LastAt < t {
				row.LastAt = &t
			}
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	for _, row := range idx {
		report.Groups = append(report.Groups, *row)
	}
	// Stable sort by score_sum desc, then post_count desc.
	for i := 1; i < len(report.Groups); i++ {
		for j := i; j > 0; j-- {
			a, b := report.Groups[j-1], report.Groups[j]
			if a.ScoreSum < b.ScoreSum || (a.ScoreSum == b.ScoreSum && a.PostCount < b.PostCount) {
				report.Groups[j-1], report.Groups[j] = b, a
			} else {
				break
			}
		}
	}
	return report, nil
}
