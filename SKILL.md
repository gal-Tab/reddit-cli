---
name: pp-reddit
description: "Read-only Reddit CLI with an offline SQLite mirror, FTS5 search, and velocity-tracking commands. Trigger phrases: `what's hot on reddit`, `search reddit for`, `reddit thread about`, `what is r/<sub> saying about`, `reddit user activity`, `track reddit topic`, `reddit watchlist`, `use reddit`, `run reddit`."
author: "user"
license: "Apache-2.0"
argument-hint: "<command> [args] | install cli|mcp"
allowed-tools: "Read Bash"
metadata:
  openclaw:
    requires:
      bins:
        - reddit-cli
---

# Reddit — Printing Press CLI

## Prerequisites: Install the CLI

This skill drives the `reddit-cli` binary. **You must verify the CLI is installed before invoking any command from this skill.** If it is missing, install it first:

1. Install via the Printing Press installer:
   ```bash
   npx -y @mvanhorn/printing-press install reddit --cli-only
   ```
2. Verify: `reddit-cli --version`
3. Ensure `$GOPATH/bin` (or `$HOME/go/bin`) is on `$PATH`.

If the `npx` install fails before this CLI has a public-library category, install Node or use the category-specific Go fallback after publish.

If `--version` reports "command not found" after install, the install step did not put the binary on `$PATH`. Do not proceed with skill commands until verification succeeds.

Wraps every read endpoint of Reddit's `.json` surface (subreddit listings, post+comments, user activity, search, multireddits, frontpage, live threads) and persists them to a local SQLite + FTS5 store. That mirror powers a set of commands that the public API alone can't answer: `velocity` finds posts breaking out in the last hour, `pulse` compares topic velocity across subreddits this week vs last, `sweep` aggregates a topic across N subs with crosspost dedup, `cascade` walks a post's crosspost graph, `controversial-now` finds spicy threads by comment-to-upvote ratio, and `watchlist` turns the open-12-tabs ritual into one `refresh` + `diff`. Two transports: anonymous polite-UA HTTP (always works, ≤8 QPM) and Chrome-cookie-replay (lifts to ≤80 QPM, unlocks personalized frontpage / subscriptions / saved-list reads). No OAuth, no app registration, read-only by design.

## When to Use This CLI

Reach for reddit-cli when you need to ingest Reddit programmatically without setting up an OAuth app — for daily community-pulse scans, breaking-story sweeps across multiple subs, user activity research, or any agentic workflow that needs Reddit data in JSON form with proper rate-limit handling. It's the right tool when you want compound queries (`velocity`, `pulse`, `sweep`, `watchlist diff`) the public API can't answer alone, or when you'd otherwise be running a one-off Python script with PRAW. Skip it for write actions (post, vote, comment) or moderation work — those need OAuth and aren't part of the surface.

## When Not to Use This CLI

Do not activate this CLI for requests that require creating, updating, deleting, publishing, commenting, upvoting, inviting, ordering, sending messages, booking, purchasing, or changing remote state. This printed CLI exposes read-only commands for inspection, export, sync, and analysis.

## Unique Capabilities

These capabilities aren't available in any other tool for this API.

### Real-time community pulse
- **`velocity`** — Re-rank a subreddit's rising listing by upvotes-per-minute computed from local snapshot deltas, not by Reddit's own opaque rising algorithm.

  _Use this when you need to find posts that broke out in the last hour, not posts that have been quietly accumulating for a day. The 'rising' tab is opaque; this gives you the actual rate._

  ```bash
  reddit-cli velocity wallstreetbets --window 1h --min-score 100 --json
  ```
- **`pulse`** — Compare mentions-per-hour of a topic this window vs the prior window across N subreddits, surfacing which communities are heating up on it.

  _Use this when you want to know which communities are getting agitated about a topic right now compared to last week — the early signal before a story breaks mainstream._

  ```bash
  reddit-cli pulse 'gpt-5' --subs r/ChatGPT,r/MachineLearning,r/OpenAI --vs-window 7d --json
  ```

### Cross-sub aggregation
- **`sweep`** — Top posts mentioning a topic across N subreddits in a window, deduplicated via the local crosspost-edges table so the same viral post counted once.

  _Use this for breaking-story sweeps when you need the highest-signal threads across multiple communities without 80% duplicates from crossposts._

  ```bash
  reddit-cli sweep 'antitrust' --subs r/technology,r/law,r/politics --since 24h --min-score 50 --json --select title,subreddit,score,permalink
  ```
- **`user activity`** — All posts and comments by a user in a window, grouped by subreddit with karma sums, post counts, and first/last activity timestamps per sub.

  _Use this to characterize what someone has been doing across Reddit — researcher use, mod investigation, or just understanding a power user's footprint without scrolling._

  ```bash
  reddit-cli user activity spez --since 30d --group-by sub --json
  ```

### Reddit-specific structure
- **`cascade`** — Recursive walk of a post's crosspost graph rendered as a tree (or `--graph` DOT) showing every downstream crosspost with its subreddit, score, and age.

  _Use this to trace how a viral post propagated across subreddits. Essential for moderation research and any 'where did this story break out' question._

  ```bash
  reddit-cli cascade abc123 --depth 3 --json
  ```
- **`controversial-now`** — Posts in a subreddit window where comment-count divided by score is unusually high — the 'fight in the parking lot' signal that Reddit's own controversial sort fails to surface in the recent window.

  _Use this when you want the spicy current threads in a sub, not the all-time controversial archive. Powers competitive intel and community-temperature reads._

  ```bash
  reddit-cli controversial-now politics --min-ratio 0.5 --since 24h --json
  ```

### Daily driver
- **`watchlist`** — A named bag of subreddits and users; one command refreshes them all and the diff sub-command reports what's new and what's rising since the last sync, grouped by entry.

  _Use this as the daily-driver entrypoint. Replaces the open-12-tabs-and-Ctrl-F ritual every persona does today; agents use it to compute deltas between checks instead of re-fetching._

  ```bash
  reddit-cli watchlist add r/golang && reddit-cli watchlist refresh && reddit-cli watchlist diff --json
  ```

## Command Reference

**comment** — Comments by ID

- `reddit-cli comment <id>` — Look up a comment by its t1_ fullname

**discover** — Discover subreddits and users

- `reddit-cli discover subreddit_search` — Search for subreddits by name or description
- `reddit-cli discover subreddits` — Popular subreddits
- `reddit-cli discover subreddits_new` — Newly created subreddits
- `reddit-cli discover users` — Popular users

**frontpage** — Reddit frontpage and best-of

- `reddit-cli frontpage best` — Get the Reddit best listing
- `reddit-cli frontpage get` — Get the Reddit frontpage (anonymous = best-of; cookie = personalized)

**live** — Live threads

- `reddit-cli live get` — Live thread updates
- `reddit-cli live info` — Live thread metadata: title, description, state, viewer count

**me** — Authenticated reads (cookie mode only — run `auth login --chrome` first)

- `reddit-cli me downvoted` — Posts the user has downvoted (cookie mode only)
- `reddit-cli me hidden` — Posts the user has hidden (cookie mode only)
- `reddit-cli me saved` — Posts and comments the user has saved (cookie mode only)
- `reddit-cli me subscriptions` — Subreddits the authenticated user is subscribed to (cookie mode only)
- `reddit-cli me upvoted` — Posts the user has upvoted (cookie mode only)

**multi** — Multireddit feeds

- `reddit-cli multi <username> <multiname>` — Read a multireddit feed (combined listing across the multi's subs)

**post** — Posts and their comment trees

- `reddit-cli post get` — Fetch a post and its full comment tree (flattened with depth+score in JSON output)
- `reddit-cli post info` — Look up posts by ID(s) without their comment trees

**search_posts** — Site-wide post search across all of Reddit

- `reddit-cli search_posts <q>` — Search posts across all subreddits

**subreddit** — Subreddit listings, metadata, rules, moderators, wiki, and scoped search

- `reddit-cli subreddit controversial` — Controversial listing for a subreddit; --t selects timeframe
- `reddit-cli subreddit hot` — Hot listing for a subreddit
- `reddit-cli subreddit info` — Subreddit metadata: subscribers, description, created, over_18, type
- `reddit-cli subreddit moderators` — Subreddit moderator list
- `reddit-cli subreddit new` — New listing for a subreddit
- `reddit-cli subreddit rising` — Rising listing for a subreddit (Reddit's own rising algorithm — see also `velocity`)
- `reddit-cli subreddit rules` — Subreddit rules
- `reddit-cli subreddit search` — Subreddit-scoped search
- `reddit-cli subreddit top` — Top listing for a subreddit; --t selects timeframe
- `reddit-cli subreddit wiki` — Read a wiki page from a subreddit

**user** — User profile, activity, and trophies

- `reddit-cli user about` — User metadata: karma, account age, mod status, gold status
- `reddit-cli user comments` — Comments by this user
- `reddit-cli user overview` — User overview: combined posts and comments in reverse-chrono
- `reddit-cli user posts` — Posts submitted by this user
- `reddit-cli user trophies` — Trophies awarded to this user


### Finding the right command

When you know what you want to do but not which command does it, ask the CLI directly:

```bash
reddit-cli which "<capability in your own words>"
```

`which` resolves a natural-language capability query to the best matching command from this CLI's curated feature index. Exit code `0` means at least one match; exit code `2` means no confident match — fall back to `--help` or use a narrower query.

## Recipes


### Daily community-pulse refresh

```bash
reddit-cli watchlist refresh --json
```

After watchlist add-ing your subs and users once, this is the every-morning ritual. Refresh fetches; pair with 'watchlist diff --json' to see what's new since the last refresh, grouped by entry.

### Find spicy threads in r/X right now

```bash
reddit-cli controversial-now politics --min-ratio 0.5 --since 24h --limit 10 --json
```

Surfaces the top 10 posts in r/politics where comment-count divided by score exceeds 0.5 — the high-comment-low-score 'fight in the parking lot' signal. Run watchlist refresh on r/politics first to populate.

### Trace how a story crossposted

```bash
reddit-cli cascade abc123 --depth 3 --graph
```

Walks the local crosspost_edges table from the given post and renders the propagation tree as Graphviz DOT. Pipe into 'dot -Tpng > cascade.png' to render. Edges are populated when posts are fetched via post get / sweep / watchlist refresh.

### Profile a power user across all subs

```bash
reddit-cli user activity spez --since 90d --group-by sub --json
```

Walks the user's overview cursor for 90 days, then groups locally by subreddit. Returns where they've been most active by both volume and karma weight.

### Agent-native deeply-nested response narrowing

```bash
reddit-cli post get abc123 --json --select data.children.data.body,data.children.data.score,data.children.data.depth
```

A /comments/<id>.json response is deeply nested (the [Listing(post), Listing(comments)] two-element array). The --select flag with dotted paths slices out just the comment body+score+depth, dropping the surrounding Listing envelope and post-level fields. Critical for keeping agent context lean.

## Auth Setup

Reddit-cli runs in two modes and picks the right one per request. By default it's fully anonymous: a polite User-Agent (`reddit-cli:v0.1.0 (by /u/<your-handle>)`), an adaptive ≤8 QPM throttle, and the well-known `.json`-suffix endpoints — works the moment you install it, no setup. For richer reads (personalized frontpage, your subscriptions, your saved/upvoted/downvoted lists, lifted ≤80 QPM ceiling), run `auth login --chrome` once: it captures your reddit.com session cookie from your default Chrome profile, no OAuth app, no client_id, no client_secret. `doctor` always tells you which mode is active and what's reachable. There is no API-key flow because there is no public API key for Reddit anymore — Reddit killed self-service keys in late 2025. Cookie or anonymous, those are the two paths.

Run `reddit-cli doctor` to verify setup.

## Agent Mode

Add `--agent` to any command. Expands to: `--json --compact --no-input --no-color --yes`.

- **Pipeable** — JSON on stdout, errors on stderr
- **Filterable** — `--select` keeps a subset of fields. Dotted paths descend into nested structures; arrays traverse element-wise. Critical for keeping context small on verbose APIs:

  ```bash
  reddit-cli comment mock-value --agent --select id,name,status
  ```
- **Previewable** — `--dry-run` shows the request without sending
- **Offline-friendly** — sync/search commands can use the local SQLite store when available
- **Non-interactive** — never prompts, every input is a flag
- **Read-only** — do not use this CLI for create, update, delete, publish, comment, upvote, invite, order, send, or other mutating requests

### Response envelope

Commands that read from the local store or the API wrap output in a provenance envelope:

```json
{
  "meta": {"source": "live" | "local", "synced_at": "...", "reason": "..."},
  "results": <data>
}
```

Parse `.results` for data and `.meta.source` to know whether it's live or local. A human-readable `N results (live)` summary is printed to stderr only when stdout is a terminal — piped/agent consumers get pure JSON on stdout.

## Agent Feedback

When you (or the agent) notice something off about this CLI, record it:

```
reddit-cli feedback "the --since flag is inclusive but docs say exclusive"
reddit-cli feedback --stdin < notes.txt
reddit-cli feedback list --json --limit 10
```

Entries are stored locally at `~/.reddit-cli/feedback.jsonl`. They are never POSTed unless `REDDIT_FEEDBACK_ENDPOINT` is set AND either `--send` is passed or `REDDIT_FEEDBACK_AUTO_SEND=true`. Default behavior is local-only.

Write what *surprised* you, not a bug report. Short, specific, one line: that is the part that compounds.

## Output Delivery

Every command accepts `--deliver <sink>`. The output goes to the named sink in addition to (or instead of) stdout, so agents can route command results without hand-piping. Three sinks are supported:

| Sink | Effect |
|------|--------|
| `stdout` | Default; write to stdout only |
| `file:<path>` | Atomically write output to `<path>` (tmp + rename) |
| `webhook:<url>` | POST the output body to the URL (`application/json` or `application/x-ndjson` when `--compact`) |

Unknown schemes are refused with a structured error naming the supported set. Webhook failures return non-zero and log the URL + HTTP status on stderr.

## Named Profiles

A profile is a saved set of flag values, reused across invocations. Use it when a scheduled agent calls the same command every run with the same configuration - HeyGen's "Beacon" pattern.

```
reddit-cli profile save briefing --json
reddit-cli --profile briefing comment mock-value
reddit-cli profile list --json
reddit-cli profile show briefing
reddit-cli profile delete briefing --yes
```

Explicit flags always win over profile values; profile values win over defaults. `agent-context` lists all available profiles under `available_profiles` so introspecting agents discover them at runtime.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Usage error (wrong arguments) |
| 3 | Resource not found |
| 4 | Authentication required |
| 5 | API error (upstream issue) |
| 7 | Rate limited (wait and retry) |
| 10 | Config error |

## Argument Parsing

Parse `$ARGUMENTS`:

1. **Empty, `help`, or `--help`** → show `reddit-cli --help` output
2. **Starts with `install`** → ends with `mcp` → MCP installation; otherwise → see Prerequisites above
3. **Anything else** → Direct Use (execute as CLI command with `--agent`)

## MCP Server Installation

Install the MCP binary from this CLI's published public-library entry or pre-built release, then register it:

```bash
claude mcp add reddit-cli-mcp -- reddit-cli-mcp
```

Verify: `claude mcp list`

## Direct Use

1. Check if installed: `which reddit-cli`
   If not found, offer to install (see Prerequisites at the top of this skill).
2. Match the user query to the best command from the Unique Capabilities and Command Reference above.
3. Execute with the `--agent` flag:
   ```bash
   reddit-cli <command> [subcommand] [args] --agent
   ```
4. If ambiguous, drill into subcommand help: `reddit-cli <command> --help`.
