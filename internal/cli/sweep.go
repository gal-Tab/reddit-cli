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

func newSweepCmd(flags *rootFlags) *cobra.Command {
	var subsCSV string
	var sinceStr string
	var minScore int
	var limit int
	var dbPath string

	cmd := &cobra.Command{
		Use:   "sweep <topic>",
		Short: "Top posts mentioning a topic across N subreddits, deduped via crosspost edges.",
		Long: strings.Trim(`
Fans out /r/<sub>/search.json per requested subreddit, persists results
locally, then dedups viral cascades via the crosspost_edges table —
keeping only the highest-scoring node of each cascade. Returns the
top-scoring posts within the time window.

Use --subs to specify which subreddits to sweep (comma-separated).
Use --since to bound the time window (default 24h).
Use --min-score to filter low-signal results.
`, "\n"),
		Example: strings.Trim(`
  reddit-cli sweep 'rust async' --subs golang,rust,programming --since 24h --min-score 50 --json
  reddit-cli sweep antitrust --subs technology,law,politics --since 7d --limit 20
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			topic := strings.TrimSpace(args[0])
			if subsCSV == "" {
				return fmt.Errorf("--subs is required (comma-separated subreddit names)")
			}
			subs := splitAndCleanSubs(subsCSV)
			since, err := parseHumanDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would sweep %q across %v since=%s min-score=%d\n", topic, subs, since, minScore)
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
			tFilter := durationToRedditT(since)
			// Fan out one search.json per sub.
			for _, sub := range subs {
				path := "/r/" + sub + "/search.json"
				params := map[string]string{
					"q":           topic,
					"restrict_sr": "on",
					"sort":        "top",
					"t":           tFilter,
					"limit":       "100",
				}
				if _, err := fetchAndPersist(cmd.Context(), c, db, path, params); err != nil {
					// Log per-sub failure but keep sweeping.
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: search r/%s failed: %v\n", sub, err)
					continue
				}
			}
			// Dedupe + rank from local store.
			results, err := computeSweep(cmd.Context(), db.DB(), topic, subs, since, minScore, limit)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), results, flags)
		},
	}
	cmd.Flags().StringVar(&subsCSV, "subs", "", "Comma-separated subreddit names without r/ prefix (required)")
	cmd.Flags().StringVar(&sinceStr, "since", "24h", "Time window (e.g. 1h, 24h, 7d, 30d)")
	cmd.Flags().IntVar(&minScore, "min-score", 0, "Minimum post score")
	cmd.Flags().IntVar(&limit, "limit", 25, "Max results to return after dedup")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

type sweepResult struct {
	ID                 string `json:"id"`
	Subreddit          string `json:"subreddit"`
	Title              string `json:"title"`
	Author             string `json:"author"`
	Permalink          string `json:"permalink"`
	Score              int    `json:"score"`
	NumComments        int    `json:"num_comments"`
	CrosspostsAbsorbed int    `json:"crossposts_absorbed,omitempty"`
}

func computeSweep(ctx context.Context, db *sql.DB, topic string, subs []string, since time.Duration, minScore, limit int) ([]sweepResult, error) {
	pattern := "%" + strings.ToLower(topic) + "%"
	winSec := int64(since.Seconds())
	if winSec <= 0 {
		winSec = 24 * 60 * 60
	}
	// Build IN clause manually because database/sql doesn't expand slices.
	if len(subs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(subs))
	args := []any{pattern, pattern}
	for i, s := range subs {
		placeholders[i] = "?"
		args = append(args, s)
	}
	args = append(args, winSec, minScore)
	q := fmt.Sprintf(`
		SELECT data FROM resources
		 WHERE resource_type = 'post'
		   AND (lower(json_extract(data, '$.title')) LIKE ? OR lower(json_extract(data, '$.selftext')) LIKE ?)
		   AND lower(json_extract(data, '$.subreddit')) IN (%s)
		   AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >= (CAST(strftime('%%s','now') AS INTEGER) - ?)
		   AND CAST(json_extract(data, '$.score') AS INTEGER) >= ?
		 ORDER BY CAST(json_extract(data, '$.score') AS INTEGER) DESC`,
		strings.ToLower(strings.Join(placeholdersLowerWrap(placeholders, len(subs)), ",")))
	// Lowercase each sub argument for the IN clause comparison.
	for i := 2; i < 2+len(subs); i++ {
		args[i] = strings.ToLower(args[i].(string))
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sweep query: %w", err)
	}
	defer rows.Close()
	var posts []redditPost
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p redditPost
		if err := json.Unmarshal([]byte(data), &p); err == nil && p.ID != "" {
			posts = append(posts, p)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Dedup via crosspost_edges: if any post in our set is the parent of
	// another in our set, drop the lower-scoring child.
	dropped := make(map[string]bool)
	scoreOf := make(map[string]int)
	for _, p := range posts {
		scoreOf[p.Name] = p.Score
	}
	if len(posts) > 1 {
		// Pull edges that touch any of our posts.
		edgeRows, err := db.QueryContext(ctx,
			`SELECT parent_id, child_id FROM crosspost_edges WHERE parent_id = ? OR child_id = ?`,
			"PLACEHOLDER", "PLACEHOLDER") // Stub query rebuilt below
		if err == nil {
			edgeRows.Close()
		}
		// Build a real query with the actual IDs.
		ids := make([]any, 0, len(posts))
		idPH := make([]string, 0, len(posts))
		for _, p := range posts {
			ids = append(ids, p.Name)
			idPH = append(idPH, "?")
		}
		eq := fmt.Sprintf(`SELECT parent_id, child_id FROM crosspost_edges
		                    WHERE parent_id IN (%s) AND child_id IN (%s)`,
			strings.Join(idPH, ","), strings.Join(idPH, ","))
		eArgs := append([]any{}, ids...)
		eArgs = append(eArgs, ids...)
		erows, err := db.QueryContext(ctx, eq, eArgs...)
		if err == nil {
			defer erows.Close()
			absorbed := make(map[string]int)
			for erows.Next() {
				var parent, child string
				if err := erows.Scan(&parent, &child); err != nil {
					continue
				}
				if scoreOf[parent] >= scoreOf[child] {
					dropped[child] = true
					absorbed[parent]++
				} else {
					dropped[parent] = true
					absorbed[child]++
				}
			}
			// Stamp absorbed counts onto results.
			results := make([]sweepResult, 0, len(posts))
			for _, p := range posts {
				if dropped[p.Name] {
					continue
				}
				results = append(results, sweepResult{
					ID:                 p.ID,
					Subreddit:          p.Subreddit,
					Title:              p.Title,
					Author:             p.Author,
					Permalink:          "https://www.reddit.com" + p.Permalink,
					Score:              p.Score,
					NumComments:        p.NumComments,
					CrosspostsAbsorbed: absorbed[p.Name],
				})
			}
			if limit > 0 && len(results) > limit {
				results = results[:limit]
			}
			return results, nil
		}
	}
	// No dedup needed (or edges query failed).
	results := make([]sweepResult, 0, len(posts))
	for _, p := range posts {
		results = append(results, sweepResult{
			ID:          p.ID,
			Subreddit:   p.Subreddit,
			Title:       p.Title,
			Author:      p.Author,
			Permalink:   "https://www.reddit.com" + p.Permalink,
			Score:       p.Score,
			NumComments: p.NumComments,
		})
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// placeholdersLowerWrap is a leftover from refactoring; present only to
// keep IN-clause expansion explicit.
func placeholdersLowerWrap(ph []string, _ int) []string { return ph }

// durationToRedditT translates a Go duration to Reddit's `t=` filter.
// Reddit only accepts hour|day|week|month|year|all. Choose the smallest
// bucket that contains the requested window so we don't miss results.
func durationToRedditT(d time.Duration) string {
	switch {
	case d <= time.Hour:
		return "hour"
	case d <= 24*time.Hour:
		return "day"
	case d <= 7*24*time.Hour:
		return "week"
	case d <= 30*24*time.Hour:
		return "month"
	case d <= 365*24*time.Hour:
		return "year"
	default:
		return "all"
	}
}
