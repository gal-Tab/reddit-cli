package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EnsureRedditExtras creates the three Reddit-specific tables that the
// novel transcendence commands depend on:
//
//   - snapshots: append-only (post_id, observed_at, score, num_comments)
//     used by `velocity` to compute upvotes-per-minute from successive
//     observations of the same post. PK on (post_id, observed_at) makes
//     re-recording at the same instant a no-op via INSERT OR IGNORE.
//
//   - crosspost_edges: directed (parent_id, child_id) pairs used by
//     `cascade` to walk a viral repost tree and by `sweep` to dedup the
//     same submission appearing in multiple subs. PK prevents duplicates.
//
//   - watchlist: bookmarked subreddits and users with last_synced_at /
//     last_count. PK on (kind, target).
//
// Idempotent: safe to call on every command invocation. Uses CREATE TABLE
// IF NOT EXISTS so it never racing-creates with another connection.
func EnsureRedditExtras(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS snapshots (
			post_id      TEXT    NOT NULL,
			observed_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			score        INTEGER NOT NULL,
			num_comments INTEGER NOT NULL,
			PRIMARY KEY (post_id, observed_at)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_post_id ON snapshots(post_id)`,
		`CREATE TABLE IF NOT EXISTS crosspost_edges (
			parent_id  TEXT NOT NULL,
			child_id   TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (parent_id, child_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_crosspost_edges_child ON crosspost_edges(child_id)`,
		`CREATE TABLE IF NOT EXISTS watchlist (
			kind            TEXT NOT NULL,
			target          TEXT NOT NULL,
			added_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_synced_at  DATETIME,
			last_count      INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (kind, target)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("ensure reddit extras: %w", err)
		}
	}
	// Additive column migrations for DBs created by older binaries.
	// SQLite reports "duplicate column name" when the column already exists;
	// treat that as success since the goal is convergence, not insertion.
	addColumns := []string{
		`ALTER TABLE watchlist ADD COLUMN last_count INTEGER NOT NULL DEFAULT 0`,
	}
	for _, s := range addColumns {
		if _, err := db.ExecContext(ctx, s); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("ensure reddit extras (alter): %w", err)
		}
	}
	return nil
}

// RecordSnapshot appends one observation of a post's score and comment
// count. Same (post_id, observed_at) pair becomes a no-op via INSERT OR
// IGNORE — a tight loop that calls twice in the same second won't error.
//
// observed_at is set to CURRENT_TIMESTAMP by the column default so the
// caller doesn't pass a clock.
func RecordSnapshot(ctx context.Context, db *sql.DB, postID string, score, numComments int) error {
	postID = strings.TrimSpace(postID)
	if postID == "" {
		return fmt.Errorf("record snapshot: empty post_id")
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO snapshots (post_id, score, num_comments) VALUES (?, ?, ?)`,
		postID, score, numComments)
	if err != nil {
		return fmt.Errorf("record snapshot: %w", err)
	}
	return nil
}

// RecordCrosspostEdge inserts a directed parent→child edge. Empty IDs and
// self-loops are rejected silently (returning nil) — these arise from
// missing crosspost_parent fields and shouldn't fail the calling command.
func RecordCrosspostEdge(ctx context.Context, db *sql.DB, parentID, childID string) error {
	parentID = strings.TrimSpace(parentID)
	childID = strings.TrimSpace(childID)
	if parentID == "" || childID == "" || parentID == childID {
		return nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO crosspost_edges (parent_id, child_id) VALUES (?, ?)`,
		parentID, childID)
	if err != nil {
		return fmt.Errorf("record crosspost edge: %w", err)
	}
	return nil
}

// WatchlistEntry mirrors one row of the watchlist table. LastSyncedAt is
// nullable — newly added entries have never been synced.
type WatchlistEntry struct {
	Kind         string     `json:"kind"`
	Target       string     `json:"target"`
	AddedAt      time.Time  `json:"added_at"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastCount    int        `json:"last_count"`
}

// WatchlistAdd inserts a (kind, target) pair if absent. Returns true when
// a new row was created, false when the entry already existed. kind must
// be "subreddit" or "user".
func WatchlistAdd(ctx context.Context, db *sql.DB, kind, target string) (bool, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	target = strings.TrimSpace(target)
	if kind != "subreddit" && kind != "user" {
		return false, fmt.Errorf("watchlist add: kind must be 'subreddit' or 'user', got %q", kind)
	}
	if target == "" {
		return false, fmt.Errorf("watchlist add: empty target")
	}
	res, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO watchlist (kind, target) VALUES (?, ?)`, kind, target)
	if err != nil {
		return false, fmt.Errorf("watchlist add: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("watchlist add: rows affected: %w", err)
	}
	return n > 0, nil
}

// WatchlistRemove deletes one entry. Returns true if a row was deleted,
// false if no such (kind, target) existed.
func WatchlistRemove(ctx context.Context, db *sql.DB, kind, target string) (bool, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	target = strings.TrimSpace(target)
	if kind == "" || target == "" {
		return false, fmt.Errorf("watchlist remove: kind and target required")
	}
	res, err := db.ExecContext(ctx,
		`DELETE FROM watchlist WHERE kind = ? AND target = ?`, kind, target)
	if err != nil {
		return false, fmt.Errorf("watchlist remove: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("watchlist remove: rows affected: %w", err)
	}
	return n > 0, nil
}

// WatchlistList returns every entry, oldest-added first. Empty result is
// returned as a non-nil empty slice so callers can encode []WatchlistEntry{}
// directly to a JSON array.
func WatchlistList(ctx context.Context, db *sql.DB) ([]WatchlistEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT kind, target, added_at, last_synced_at, last_count FROM watchlist ORDER BY added_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("watchlist list: %w", err)
	}
	defer rows.Close()
	out := make([]WatchlistEntry, 0)
	for rows.Next() {
		var (
			e      WatchlistEntry
			synced sql.NullTime
		)
		if err := rows.Scan(&e.Kind, &e.Target, &e.AddedAt, &synced, &e.LastCount); err != nil {
			return nil, fmt.Errorf("watchlist list scan: %w", err)
		}
		if synced.Valid {
			t := synced.Time
			e.LastSyncedAt = &t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("watchlist list rows: %w", err)
	}
	return out, nil
}

// WatchlistMarkSynced stamps last_synced_at = now and last_count = count
// for one entry. No-op (returns nil) if the entry doesn't exist — the
// refresh path shouldn't fail just because someone removed an entry mid-run.
func WatchlistMarkSynced(ctx context.Context, db *sql.DB, kind, target string, count int) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	target = strings.TrimSpace(target)
	if kind == "" || target == "" {
		return fmt.Errorf("watchlist mark synced: kind and target required")
	}
	_, err := db.ExecContext(ctx,
		`UPDATE watchlist SET last_synced_at = CURRENT_TIMESTAMP, last_count = ?
		   WHERE kind = ? AND target = ?`, count, kind, target)
	if err != nil {
		return fmt.Errorf("watchlist mark synced: %w", err)
	}
	return nil
}
