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

func newControversialNowCmd(flags *rootFlags) *cobra.Command {
	var minRatio float64
	var sinceStr string
	var limit int
	var dbPath string

	cmd := &cobra.Command{
		Use:   "controversial-now <subreddit>",
		Short: "Posts in a subreddit window where comment-to-upvote ratio is unusually high.",
		Long: strings.Trim(`
Reddit's own "controversial" sort is across all time. This command
finds the spicy threads RIGHT NOW: posts in the last <since> with a
high num_comments / score ratio (the "fight in the parking lot" signal).

Reads from the local store. Run watchlist refresh, sync, or a subreddit
listing first to populate.
`, "\n"),
		Example: strings.Trim(`
  reddit-pp-cli controversial-now politics --min-ratio 0.5 --since 24h --limit 10 --json
  reddit-pp-cli controversial-now technology --min-ratio 1.0 --since 6h
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			sub := strings.TrimPrefix(strings.TrimPrefix(args[0], "r/"), "/r/")
			since, err := parseHumanDuration(sinceStr)
			if err != nil {
				return fmt.Errorf("--since: %w", err)
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would find controversial-now in r/%s min-ratio=%.2f since=%s\n", sub, minRatio, since)
				return nil
			}
			if dbPath == "" {
				dbPath = defaultDBPath("reddit-pp-cli")
			}
			db, err := store.OpenWithContext(cmd.Context(), dbPath)
			if err != nil {
				return fmt.Errorf("opening database: %w", err)
			}
			defer db.Close()
			if err := store.EnsureRedditExtras(cmd.Context(), db.DB()); err != nil {
				return err
			}
			results, err := computeControversialNow(cmd.Context(), db.DB(), sub, since, minRatio, limit)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), results, flags)
		},
	}
	cmd.Flags().Float64Var(&minRatio, "min-ratio", 0.5, "Minimum num_comments / max(score,1) ratio")
	cmd.Flags().StringVar(&sinceStr, "since", "24h", "Time window (e.g. 6h, 24h, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 25, "Max results")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

type controversialResult struct {
	ID          string  `json:"id"`
	Subreddit   string  `json:"subreddit"`
	Title       string  `json:"title"`
	Author      string  `json:"author"`
	Permalink   string  `json:"permalink"`
	Score       int     `json:"score"`
	NumComments int     `json:"num_comments"`
	Ratio       float64 `json:"controversy_ratio"`
}

func computeControversialNow(ctx context.Context, db *sql.DB, sub string, since time.Duration, minRatio float64, limit int) ([]controversialResult, error) {
	winSec := int64(since.Seconds())
	if winSec <= 0 {
		winSec = 24 * 60 * 60
	}
	q := `SELECT data FROM resources
	      WHERE resource_type = 'post'
	        AND lower(json_extract(data, '$.subreddit')) = lower(?)
	        AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >=
	            (CAST(strftime('%s','now') AS INTEGER) - ?)`
	rows, err := db.QueryContext(ctx, q, sub, winSec)
	if err != nil {
		return nil, fmt.Errorf("controversial query: %w", err)
	}
	defer rows.Close()
	results := make([]controversialResult, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p redditPost
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			continue
		}
		if p.ID == "" {
			continue
		}
		denom := p.Score
		if denom < 1 {
			denom = 1
		}
		ratio := float64(p.NumComments) / float64(denom)
		if ratio < minRatio {
			continue
		}
		results = append(results, controversialResult{
			ID:          p.ID,
			Subreddit:   p.Subreddit,
			Title:       p.Title,
			Author:      p.Author,
			Permalink:   "https://www.reddit.com" + p.Permalink,
			Score:       p.Score,
			NumComments: p.NumComments,
			Ratio:       roundTo(ratio, 2),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Sort by ratio desc, score desc as secondary.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a, b := results[j-1], results[j]
			if a.Ratio < b.Ratio || (a.Ratio == b.Ratio && a.Score < b.Score) {
				results[j-1], results[j] = b, a
			} else {
				break
			}
		}
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}
