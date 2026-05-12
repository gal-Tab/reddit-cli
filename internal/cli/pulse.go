package cli

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"reddit-pp-cli/internal/store"
)

func newPulseCmd(flags *rootFlags) *cobra.Command {
	var subsCSV string
	var vsWindowStr string
	var dbPath string

	cmd := &cobra.Command{
		Use:   "pulse <topic>",
		Short: "Compare a topic's mention count this window vs the prior window across N subreddits.",
		Long: strings.Trim(`
Reads from local posts already synced (via watchlist refresh, sync, or
direct subreddit fetches). Counts how many posts in each requested
subreddit mention the topic during the current window vs the prior
equal-length window. Returns a per-sub delta so you can see which
communities are heating up on the topic.

Use --vs-window to set the window length; the prior window is the same
length immediately preceding it. Default is 7d (so 7d-vs-7d).

Topic matches are case-insensitive substring on title + selftext.
`, "\n"),
		Example: strings.Trim(`
  reddit-pp-cli pulse 'gpt-5' --subs ChatGPT,MachineLearning,OpenAI --vs-window 7d --json
  reddit-pp-cli pulse antitrust --subs technology,law,politics --vs-window 24h
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			topic := strings.TrimSpace(args[0])
			if subsCSV == "" {
				return fmt.Errorf("--subs is required (comma-separated subreddit names without r/ prefix)")
			}
			subs := splitAndCleanSubs(subsCSV)
			vsWindow, err := parseHumanDuration(vsWindowStr)
			if err != nil {
				return fmt.Errorf("--vs-window: %w", err)
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would pulse %q across %v with window=%s\n", topic, subs, vsWindow)
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
			results, err := computePulse(cmd.Context(), db.DB(), topic, subs, vsWindow)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), results, flags)
		},
	}
	cmd.Flags().StringVar(&subsCSV, "subs", "", "Comma-separated subreddit names without r/ prefix (required)")
	cmd.Flags().StringVar(&vsWindowStr, "vs-window", "7d", "Window length; prior window is the same length immediately before (e.g. 24h, 7d, 1w)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

// pulseRow is one per-sub mention-delta row.
type pulseRow struct {
	Subreddit     string  `json:"subreddit"`
	MentionsNow   int     `json:"mentions_now"`
	MentionsPrior int     `json:"mentions_prior"`
	DeltaPct      float64 `json:"delta_pct"`
	WindowSeconds int64   `json:"window_seconds"`
}

func computePulse(ctx context.Context, db *sql.DB, topic string, subs []string, window time.Duration) ([]pulseRow, error) {
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	winSec := int64(window.Seconds())
	pattern := "%" + strings.ToLower(topic) + "%"
	out := make([]pulseRow, 0, len(subs))
	for _, sub := range subs {
		nowCount, err := countTopicHits(ctx, db, sub, pattern, 0, winSec)
		if err != nil {
			return nil, err
		}
		priorCount, err := countTopicHits(ctx, db, sub, pattern, winSec, winSec)
		if err != nil {
			return nil, err
		}
		var delta float64
		if priorCount > 0 {
			delta = roundTo((float64(nowCount-priorCount)/float64(priorCount))*100, 1)
		} else if nowCount > 0 {
			delta = 100 // brand-new topic for this sub
		}
		out = append(out, pulseRow{
			Subreddit:     sub,
			MentionsNow:   nowCount,
			MentionsPrior: priorCount,
			DeltaPct:      delta,
			WindowSeconds: winSec,
		})
	}
	return out, nil
}

// countTopicHits returns the count of posts in `sub` whose title or
// selftext contains `pattern` (already %-wrapped lowercase) and whose
// created_utc is between (now - offsetSec - winSec) and (now - offsetSec).
func countTopicHits(ctx context.Context, db *sql.DB, sub, pattern string, offsetSec, winSec int64) (int, error) {
	q := `SELECT COUNT(*) FROM resources
	      WHERE resource_type = 'post'
	        AND lower(json_extract(data, '$.subreddit')) = lower(?)
	        AND (lower(json_extract(data, '$.title')) LIKE ? OR lower(json_extract(data, '$.selftext')) LIKE ?)
	        AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >= (CAST(strftime('%s','now') AS INTEGER) - ? - ?)
	        AND CAST(json_extract(data, '$.created_utc') AS INTEGER) <  (CAST(strftime('%s','now') AS INTEGER) - ?)`
	var n int
	err := db.QueryRowContext(ctx, q, sub, pattern, pattern, offsetSec, winSec, offsetSec).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count topic hits in r/%s: %w", sub, err)
	}
	return n, nil
}

func splitAndCleanSubs(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "r/")
		p = strings.TrimPrefix(p, "/r/")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
