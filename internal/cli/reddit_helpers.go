// Reddit-specific helpers for hand-written novel commands. The generator
// emits one-shot endpoint mirrors that just print the API response;
// transcendence commands need to PARSE the Listing envelope, persist the
// inner children, record snapshots for velocity, and record crosspost
// edges for cascade. None of that is in the framework; this file owns it.

package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gal-Tab/reddit-cli/internal/store"
)

// redditChild is one item from a Listing's children array. Reddit wraps
// every entity in a `{kind, data}` envelope: kind is "t1" for comments,
// "t2" for accounts, "t3" for links/posts, "t5" for subreddits, etc.
type redditChild struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// redditListing is the envelope `/r/<sub>/<sort>.json`, `/search.json`,
// and most cursor-paginated endpoints return.
type redditListing struct {
	Kind string `json:"kind"`
	Data struct {
		Children []redditChild   `json:"children"`
		After    json.RawMessage `json:"after"`  // string or null
		Before   json.RawMessage `json:"before"` // string or null
	} `json:"data"`
}

// redditPost is the high-gravity field subset every novel command needs.
// We pull these out of `data` for SQL queries; the full JSON is preserved
// in the resources table for completeness.
type redditPost struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"` // fullname e.g. t3_abc
	Subreddit         string  `json:"subreddit"`
	Title             string  `json:"title"`
	Author            string  `json:"author"`
	Permalink         string  `json:"permalink"`
	URL               string  `json:"url"`
	Selftext          string  `json:"selftext"`
	Score             int     `json:"score"`
	Ups               int     `json:"ups"`
	Downs             int     `json:"downs"`
	NumComments       int     `json:"num_comments"`
	CreatedUTC        float64 `json:"created_utc"`
	Stickied          bool    `json:"stickied"`
	Distinguished     string  `json:"distinguished"`
	Locked            bool    `json:"locked"`
	Over18            bool    `json:"over_18"`
	Spoiler           bool    `json:"spoiler"`
	CrosspostParentID string  `json:"crosspost_parent"`
	UpvoteRatio       float64 `json:"upvote_ratio"`
}

// parseListing decodes a Listing response and returns each child's `data`
// JSON plus the `after` cursor string ("" when null/absent). It tolerates
// a top-level array — e.g. `/comments/<id>.json` returns
// `[Listing(post), Listing(comments)]` — by walking each element.
func parseListing(raw json.RawMessage) ([]json.RawMessage, string, error) {
	if len(raw) == 0 {
		return nil, "", nil
	}
	// Top-level array: e.g. /comments/<id>.json.
	if first := strings.TrimSpace(string(raw)); strings.HasPrefix(first, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, "", fmt.Errorf("decode listing array: %w", err)
		}
		var allChildren []json.RawMessage
		var lastAfter string
		for _, elem := range arr {
			children, after, err := parseListing(elem)
			if err != nil {
				return nil, "", err
			}
			allChildren = append(allChildren, children...)
			if after != "" {
				lastAfter = after
			}
		}
		return allChildren, lastAfter, nil
	}
	var lst redditListing
	if err := json.Unmarshal(raw, &lst); err != nil {
		return nil, "", fmt.Errorf("decode listing: %w", err)
	}
	var children []json.RawMessage
	for _, c := range lst.Data.Children {
		children = append(children, c.Data)
	}
	var after string
	if len(lst.Data.After) > 0 && string(lst.Data.After) != "null" {
		_ = json.Unmarshal(lst.Data.After, &after)
	}
	return children, after, nil
}

// extractPosts tries to parse each child as a redditPost. Children that
// don't decode (e.g. comment "more" placeholders, deleted accounts) are
// skipped silently — callers shouldn't pretend they got a post they didn't.
func extractPosts(children []json.RawMessage) []redditPost {
	var posts []redditPost
	for _, c := range children {
		var p redditPost
		if err := json.Unmarshal(c, &p); err != nil {
			continue
		}
		// A real post always has an ID. Skip placeholder envelopes.
		if p.ID == "" {
			continue
		}
		// Reddit returns the fullname in `name`; if missing, build it.
		if p.Name == "" {
			p.Name = "t3_" + p.ID
		}
		posts = append(posts, p)
	}
	return posts
}

// persistPosts upserts each post into the resources table with type
// "post", records a snapshot, and records a crosspost edge if the post
// is a crosspost. Returns the count of posts actually written.
//
// Errors from individual posts are accumulated, not fatal — a malformed
// post should not stop the rest of the listing from being persisted.
func persistPosts(ctx context.Context, db *store.Store, posts []redditPost, rawChildren []json.RawMessage) (int, error) {
	if len(posts) == 0 {
		return 0, nil
	}
	if err := store.EnsureRedditExtras(ctx, db.DB()); err != nil {
		return 0, err
	}
	// Upsert all child raw JSON in one batch.
	stored, _, err := db.UpsertBatch("post", rawChildren)
	if err != nil {
		return 0, fmt.Errorf("upsert posts: %w", err)
	}
	// Snapshots + edges per post.
	for _, p := range posts {
		if err := store.RecordSnapshot(ctx, db.DB(), p.Name, p.Score, p.NumComments); err != nil {
			return stored, err
		}
		if p.CrosspostParentID != "" {
			if err := store.RecordCrosspostEdge(ctx, db.DB(), p.CrosspostParentID, p.Name); err != nil {
				return stored, err
			}
		}
	}
	return stored, nil
}

// fetchAndPersist calls one Reddit JSON endpoint, parses the Listing,
// persists posts + snapshots + edges, and returns the post slice for any
// downstream filtering or aggregation the caller needs.
//
// Concurrency note: store ops happen serially in the calling goroutine.
// This keeps semantics straightforward for the seven novel commands at
// the cost of giving up parallelism on the persist side.
func fetchAndPersist(ctx context.Context, c apiClient, db *store.Store, path string, params map[string]string) ([]redditPost, error) {
	raw, err := c.Get(path, params)
	if err != nil {
		return nil, err
	}
	children, _, err := parseListing(raw)
	if err != nil {
		return nil, err
	}
	posts := extractPosts(children)
	if _, err := persistPosts(ctx, db, posts, children); err != nil {
		return posts, err
	}
	return posts, nil
}

// apiClient narrows the generated *client.Client to the surface the
// novel commands actually use. Lets tests stub.
type apiClient interface {
	Get(path string, params map[string]string) (json.RawMessage, error)
}

// jsonNum tolerantly extracts a numeric field from a JSON value that may
// be a number or a JSON-encoded string ("100" vs 100). Reddit is mostly
// consistent but defensive parsing is cheap.
func jsonNum(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		var f float64
		_ = json.Unmarshal([]byte(t), &f)
		return f
	}
	return 0
}

// queryPostsBySub pulls posts from the resources table filtered by
// data->>'$.subreddit' = ?, optionally created in the last `sinceSec`
// seconds. Returns the parsed redditPost slice plus the raw JSON list.
func queryPostsBySub(ctx context.Context, db *sql.DB, sub string, sinceSec int64) ([]redditPost, error) {
	q := `SELECT data FROM resources WHERE resource_type = 'post'
	      AND lower(json_extract(data, '$.subreddit')) = lower(?)`
	args := []any{sub}
	if sinceSec > 0 {
		q += ` AND CAST(json_extract(data, '$.created_utc') AS INTEGER) >=
		       (CAST(strftime('%s','now') AS INTEGER) - ?)`
		args = append(args, sinceSec)
	}
	q += ` ORDER BY CAST(json_extract(data, '$.score') AS INTEGER) DESC`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query posts by sub: %w", err)
	}
	defer rows.Close()
	var out []redditPost
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p redditPost
		if err := json.Unmarshal([]byte(data), &p); err == nil && p.ID != "" {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}
