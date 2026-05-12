package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openExtrasTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_journal_mode=WAL&_busy_timeout=2000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := EnsureRedditExtras(context.Background(), db); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	return db
}

func TestEnsureRedditExtrasIdempotent(t *testing.T) {
	db := openExtrasTestDB(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := EnsureRedditExtras(ctx, db); err != nil {
			t.Fatalf("ensure pass %d: %v", i, err)
		}
	}
	for _, table := range []string{"snapshots", "crosspost_edges", "watchlist"} {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestRecordSnapshot(t *testing.T) {
	db := openExtrasTestDB(t)
	ctx := context.Background()
	if err := RecordSnapshot(ctx, db, "t3_a", 10, 2); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Same instant — INSERT OR IGNORE should swallow it without error.
	if err := RecordSnapshot(ctx, db, "t3_a", 10, 2); err != nil {
		t.Fatalf("dup: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM snapshots WHERE post_id='t3_a'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least 1 snapshot, got %d", n)
	}
	if err := RecordSnapshot(ctx, db, "  ", 1, 1); err == nil {
		t.Fatalf("expected error on empty post_id")
	}
}

func TestRecordCrosspostEdge(t *testing.T) {
	db := openExtrasTestDB(t)
	ctx := context.Background()
	if err := RecordCrosspostEdge(ctx, db, "t3_p", "t3_c"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := RecordCrosspostEdge(ctx, db, "t3_p", "t3_c"); err != nil {
		t.Fatalf("dup: %v", err)
	}
	if err := RecordCrosspostEdge(ctx, db, "", "t3_c"); err != nil {
		t.Fatalf("empty parent should be silent: %v", err)
	}
	if err := RecordCrosspostEdge(ctx, db, "t3_p", ""); err != nil {
		t.Fatalf("empty child should be silent: %v", err)
	}
	if err := RecordCrosspostEdge(ctx, db, "t3_x", "t3_x"); err != nil {
		t.Fatalf("self-loop should be silent: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM crosspost_edges`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 edge, got %d", n)
	}
}

func TestWatchlistLifecycle(t *testing.T) {
	db := openExtrasTestDB(t)
	ctx := context.Background()

	added, err := WatchlistAdd(ctx, db, "subreddit", "golang")
	if err != nil || !added {
		t.Fatalf("first add: added=%v err=%v", added, err)
	}
	added, err = WatchlistAdd(ctx, db, "subreddit", "golang")
	if err != nil || added {
		t.Fatalf("dup add: added=%v err=%v", added, err)
	}
	if _, err := WatchlistAdd(ctx, db, "user", "spez"); err != nil {
		t.Fatalf("user add: %v", err)
	}

	entries, err := WatchlistList(ctx, db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.LastSyncedAt != nil {
			t.Fatalf("fresh entry %s/%s should have nil LastSyncedAt", e.Kind, e.Target)
		}
	}

	if err := WatchlistMarkSynced(ctx, db, "subreddit", "golang", 25); err != nil {
		t.Fatalf("mark synced: %v", err)
	}
	entries, _ = WatchlistList(ctx, db)
	var found bool
	for _, e := range entries {
		if e.Kind == "subreddit" && e.Target == "golang" {
			found = true
			if e.LastSyncedAt == nil {
				t.Fatalf("expected LastSyncedAt to be set after mark")
			}
			if e.LastCount != 25 {
				t.Fatalf("expected LastCount=25, got %d", e.LastCount)
			}
		}
	}
	if !found {
		t.Fatalf("golang entry missing after mark")
	}

	removed, err := WatchlistRemove(ctx, db, "subreddit", "golang")
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	removed, err = WatchlistRemove(ctx, db, "subreddit", "golang")
	if err != nil || removed {
		t.Fatalf("remove already-gone: removed=%v err=%v", removed, err)
	}

	// Mark-synced on missing entry is a no-op, not an error.
	if err := WatchlistMarkSynced(ctx, db, "subreddit", "ghost", 5); err != nil {
		t.Fatalf("mark synced on missing entry should not error: %v", err)
	}
}

func TestWatchlistRejectsBadInput(t *testing.T) {
	db := openExtrasTestDB(t)
	ctx := context.Background()
	if _, err := WatchlistAdd(ctx, db, "subscriber", "golang"); err == nil {
		t.Fatalf("expected error on bad kind")
	}
	if _, err := WatchlistAdd(ctx, db, "subreddit", "  "); err == nil {
		t.Fatalf("expected error on empty target")
	}
	if _, err := WatchlistRemove(ctx, db, "", "golang"); err == nil {
		t.Fatalf("expected error on empty kind in remove")
	}
}
