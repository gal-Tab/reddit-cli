package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"reddit-pp-cli/internal/store"
)

func newVelocityCmd(flags *rootFlags) *cobra.Command {
	var windowStr string
	var minScore int
	var limit int
	var dbPath string

	cmd := &cobra.Command{
		Use:   "velocity <subreddit>",
		Short: "Re-rank a subreddit's rising listing by upvotes-per-minute computed from local snapshots.",
		Long: strings.Trim(`
Re-rank a subreddit's rising listing by upvotes-per-minute computed from
the local snapshots table. Reddit's own "rising" sort is opaque; this
command makes it auditable. A post with 800 upvotes in 2 hours wins over
one with 800 in 2 days.

Calls /r/<sub>/rising.json to populate the working set, then computes
the rate from snapshots already in the local store. If only one
snapshot exists for a post, the per-minute rate is computed from
created_utc instead so the command still returns useful results on the
first run.
`, "\n"),
		Example: strings.Trim(`
  reddit-pp-cli velocity wallstreetbets --window 1h --min-score 100 --json
  reddit-pp-cli velocity golang --window 30m --limit 5
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			sub := strings.TrimPrefix(strings.TrimPrefix(args[0], "r/"), "/r/")
			window, err := parseHumanDuration(windowStr)
			if err != nil {
				return fmt.Errorf("--window: %w", err)
			}
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would compute velocity for r/%s window=%s min-score=%d limit=%d\n",
					sub, window, minScore, limit)
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
			c, err := flags.newClient()
			if err != nil {
				return err
			}
			// Populate the working set from /rising — also records snapshots
			// so even cold-start runs have at least one sample.
			path := "/r/" + sub + "/rising.json"
			if _, err := fetchAndPersist(cmd.Context(), c, db, path, map[string]string{"limit": "100"}); err != nil {
				return classifyAPIError(err, flags)
			}
			results, err := computeVelocity(cmd.Context(), db, sub, window, minScore, limit)
			if err != nil {
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), results, flags)
		},
	}
	cmd.Flags().StringVar(&windowStr, "window", "1h", "Velocity window (e.g. 30m, 1h, 6h, 1d, 1w)")
	cmd.Flags().IntVar(&minScore, "min-score", 0, "Minimum current score to include")
	cmd.Flags().IntVar(&limit, "limit", 25, "Max results to return")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path (default: ~/.local/share/reddit-pp-cli/data.db)")
	return cmd
}

// velocityResult is one row of the velocity report.
type velocityResult struct {
	ID            string  `json:"id"`
	Subreddit     string  `json:"subreddit"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	Permalink     string  `json:"permalink"`
	Score         int     `json:"score"`
	NumComments   int     `json:"num_comments"`
	UpvotesPerMin float64 `json:"upvotes_per_min"`
	WindowMin     float64 `json:"window_minutes"`
	Source        string  `json:"source"` // "snapshots" or "created_utc"
}

func computeVelocity(ctx context.Context, db *store.Store, sub string, window time.Duration, minScore, limit int) ([]velocityResult, error) {
	posts, err := queryPostsBySub(ctx, db.DB(), sub, 0)
	if err != nil {
		return nil, err
	}
	windowMin := window.Minutes()
	if windowMin <= 0 {
		windowMin = 60
	}
	var results []velocityResult
	for _, p := range posts {
		if minScore > 0 && p.Score < minScore {
			continue
		}
		// Look up earliest snapshot within `window` ago.
		var startScore int
		var startObservedSec float64
		row := db.DB().QueryRowContext(ctx,
			`SELECT score, CAST(strftime('%s', observed_at) AS REAL)
			   FROM snapshots
			  WHERE post_id = ?
			    AND observed_at >= datetime('now', '-' || ? || ' seconds')
			  ORDER BY observed_at ASC LIMIT 1`,
			p.Name, int(window.Seconds()))
		err := row.Scan(&startScore, &startObservedSec)
		nowSec := float64(time.Now().Unix())
		var rate float64
		var source string
		if err == nil && startObservedSec > 0 && nowSec > startObservedSec {
			elapsedMin := (nowSec - startObservedSec) / 60.0
			if elapsedMin > 0 {
				rate = float64(p.Score-startScore) / elapsedMin
				source = "snapshots"
			}
		} else {
			// Fall back to total-life rate from created_utc.
			if p.CreatedUTC > 0 {
				ageMin := (nowSec - p.CreatedUTC) / 60.0
				if ageMin > 0 {
					rate = float64(p.Score) / ageMin
					source = "created_utc"
				}
			}
		}
		if rate <= 0 {
			continue
		}
		results = append(results, velocityResult{
			ID:            p.ID,
			Subreddit:     p.Subreddit,
			Title:         p.Title,
			Author:        p.Author,
			Permalink:     "https://www.reddit.com" + p.Permalink,
			Score:         p.Score,
			NumComments:   p.NumComments,
			UpvotesPerMin: roundTo(rate, 2),
			WindowMin:     windowMin,
			Source:        source,
		})
	}
	// Sort descending by velocity.
	sortByVelocityDesc(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func sortByVelocityDesc(results []velocityResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j-1].UpvotesPerMin < results[j].UpvotesPerMin; j-- {
			results[j-1], results[j] = results[j], results[j-1]
		}
	}
}

func roundTo(v float64, places int) float64 {
	mul := 1.0
	for i := 0; i < places; i++ {
		mul *= 10
	}
	return float64(int(v*mul+0.5)) / mul
}
