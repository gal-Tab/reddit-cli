package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gal-Tab/reddit-cli/internal/store"
)

func newCascadeCmd(flags *rootFlags) *cobra.Command {
	var depth int
	var dbPath string
	var asGraph bool

	cmd := &cobra.Command{
		Use:   "cascade <post-id>",
		Short: "Recursive walk of a post's crosspost graph (tree or DOT).",
		Long: strings.Trim(`
Walks the crosspost_edges table starting from <post-id> (the alphanumeric
ID without the t3_ prefix). Returns each downstream crosspost with its
subreddit, score, and depth.

If --graph is set, emits Graphviz DOT instead of a JSON tree — pipe into
"dot -Tpng > cascade.png" to render.

Edges are populated when posts are fetched via post get / sweep /
watchlist refresh — not auto-populated standalone. Run those first if
the cascade is empty.
`, "\n"),
		Example: strings.Trim(`
  reddit-cli cascade abc123 --depth 3 --json
  reddit-cli cascade abc123 --depth 5 --graph
`, "\n"),
		Annotations: map[string]string{"mcp:read-only": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			id := strings.TrimPrefix(args[0], "t3_")
			if dryRunOK(flags) {
				fmt.Fprintf(cmd.OutOrStdout(), "would walk cascade from t3_%s depth=%d graph=%v\n", id, depth, asGraph)
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
			tree, err := walkCascade(cmd.Context(), db.DB(), "t3_"+id, depth)
			if err != nil {
				return err
			}
			if asGraph {
				dot := renderCascadeDOT(tree)
				_, err := fmt.Fprintln(cmd.OutOrStdout(), dot)
				return err
			}
			return printJSONFiltered(cmd.OutOrStdout(), tree, flags)
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 3, "Max recursion depth")
	cmd.Flags().BoolVar(&asGraph, "graph", false, "Emit Graphviz DOT instead of JSON")
	cmd.Flags().StringVar(&dbPath, "db", "", "Database path")
	return cmd
}

// cascadeNode is one node in the crosspost tree.
type cascadeNode struct {
	ID        string        `json:"id"`         // fullname e.g. t3_abc
	Subreddit string        `json:"subreddit"`  // empty when post not in store
	Title     string        `json:"title,omitempty"`
	Score     int           `json:"score,omitempty"`
	Depth     int           `json:"depth"`
	Children  []cascadeNode `json:"children,omitempty"`
}

func walkCascade(ctx context.Context, db *sql.DB, rootName string, maxDepth int) (cascadeNode, error) {
	if maxDepth < 0 {
		maxDepth = 0
	}
	visited := make(map[string]bool)
	root, err := buildCascadeNode(ctx, db, rootName, 0, maxDepth, visited)
	if err != nil {
		return cascadeNode{}, err
	}
	return root, nil
}

func buildCascadeNode(ctx context.Context, db *sql.DB, name string, depth, max int, visited map[string]bool) (cascadeNode, error) {
	if visited[name] {
		return cascadeNode{ID: name, Depth: depth}, nil
	}
	visited[name] = true
	node := cascadeNode{ID: name, Depth: depth}
	// Look up post metadata if we have it locally.
	id := strings.TrimPrefix(name, "t3_")
	row := db.QueryRowContext(ctx, `SELECT data FROM resources WHERE resource_type = 'post' AND id = ?`, id)
	var data string
	if err := row.Scan(&data); err == nil {
		var p redditPost
		if err := json.Unmarshal([]byte(data), &p); err == nil {
			node.Subreddit = p.Subreddit
			node.Title = p.Title
			node.Score = p.Score
		}
	}
	if depth >= max {
		return node, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT child_id FROM crosspost_edges WHERE parent_id = ?`, name)
	if err != nil {
		return node, fmt.Errorf("cascade children query: %w", err)
	}
	defer rows.Close()
	var childNames []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return node, err
		}
		childNames = append(childNames, c)
	}
	if err := rows.Err(); err != nil {
		return node, err
	}
	for _, cn := range childNames {
		child, err := buildCascadeNode(ctx, db, cn, depth+1, max, visited)
		if err != nil {
			return node, err
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

func renderCascadeDOT(root cascadeNode) string {
	var b strings.Builder
	b.WriteString("digraph cascade {\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	emitDOTNode(&b, root)
	b.WriteString("}\n")
	return b.String()
}

func emitDOTNode(b *strings.Builder, n cascadeNode) {
	label := n.ID
	if n.Subreddit != "" {
		label = fmt.Sprintf("%s\\nr/%s | %d↑", n.ID, n.Subreddit, n.Score)
	}
	fmt.Fprintf(b, "  %q [label=%q];\n", n.ID, label)
	for _, c := range n.Children {
		fmt.Fprintf(b, "  %q -> %q;\n", n.ID, c.ID)
		emitDOTNode(b, c)
	}
}
