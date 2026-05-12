# Reddit CLI

**Read-only Reddit CLI with an offline SQLite mirror, FTS5 search, and velocity-tracking commands.**

Wraps every read endpoint of Reddit's `.json` surface (subreddit listings, post+comments, user activity, search, multireddits, frontpage, live threads) and persists them to a local SQLite + FTS5 store. That mirror powers a set of commands that the public API alone can't answer: `velocity` finds posts breaking out in the last hour, `pulse` compares topic velocity across subreddits this week vs last, `sweep` aggregates a topic across N subs with crosspost dedup, `cascade` walks a post's crosspost graph, `controversial-now` finds spicy threads by comment-to-upvote ratio, and `watchlist` turns the open-12-tabs ritual into one `refresh` + `diff`. Two transports: anonymous polite-UA HTTP (always works, ≤8 QPM) and Chrome-cookie-replay (lifts to ≤80 QPM, unlocks personalized frontpage / subscriptions / saved-list reads). No OAuth, no app registration, read-only by design.

## Install

The recommended path installs both the `reddit-pp-cli` binary and the `pp-reddit` agent skill in one shot:

```bash
npx -y @mvanhorn/printing-press install reddit
```

For CLI only (no skill):

```bash
npx -y @mvanhorn/printing-press install reddit --cli-only
```


### Without Node

The generated install path is category-agnostic until this CLI is published. If `npx` is not available before publish, install Node or use the category-specific Go fallback from the public-library entry after publish.

### Pre-built binary

Download a pre-built binary for your platform from the [latest release](https://github.com/mvanhorn/printing-press-library/releases/tag/reddit-current). On macOS, clear the Gatekeeper quarantine: `xattr -d com.apple.quarantine <binary>`. On Unix, mark it executable: `chmod +x <binary>`.

<!-- pp-hermes-install-anchor -->
## Install for Hermes

From the Hermes CLI:

```bash
hermes skills install mvanhorn/printing-press-library/cli-skills/pp-reddit --force
```

Inside a Hermes chat session:

```bash
/skills install mvanhorn/printing-press-library/cli-skills/pp-reddit --force
```

## Install for OpenClaw

Tell your OpenClaw agent (copy this):

```
Install the pp-reddit skill from https://github.com/mvanhorn/printing-press-library/tree/main/cli-skills/pp-reddit. The skill defines how its required CLI can be installed.
```

## Optional: API Key

**All core commands work without setup.** The API key below is only needed to unlock additional features.

Reddit-pp-cli runs in two modes and picks the right one per request. By default it's fully anonymous: a polite User-Agent (`reddit-pp-cli:v0.1.0 (by /u/<your-handle>)`), an adaptive ≤8 QPM throttle, and the well-known `.json`-suffix endpoints — works the moment you install it, no setup. For richer reads (personalized frontpage, your subscriptions, your saved/upvoted/downvoted lists, lifted ≤80 QPM ceiling), run `auth login --chrome` once: it captures your reddit.com session cookie from your default Chrome profile, no OAuth app, no client_id, no client_secret. `doctor` always tells you which mode is active and what's reachable. There is no API-key flow because there is no public API key for Reddit anymore — Reddit killed self-service keys in late 2025. Cookie or anonymous, those are the two paths.

## Quick Start

```bash
# Bookmark a sub. The watchlist replaces the open-12-tabs ritual; everything else reads from it.
reddit-pp-cli watchlist add golang


# Pull hot for every watchlist entry into the local SQLite store. This is the actual sync entry point for Reddit because sync without a target list is meaningless.
reddit-pp-cli watchlist refresh --limit 25


# What's new since the last refresh, grouped by entry. The daily-driver answer.
reddit-pp-cli watchlist diff --json


# Find posts in r/golang that gained at least 50 upvotes in the last hour, ranked by upvotes-per-minute from local snapshots. Flagship transcendence query.
reddit-pp-cli velocity golang --window 1h --min-score 50 --json


# Cross-sub topic sweep with crosspost dedup. Highest-signal threads about a topic across N subs in a window.
reddit-pp-cli sweep 'rust async' --subs golang,rust,programming --since 24h --json


# Health check: confirms anonymous mode is working and reports whether a cookie session is captured. Run 'auth login --chrome' separately to capture a session.
reddit-pp-cli doctor

```

## Unique Features

These capabilities aren't available in any other tool for this API.

### Real-time community pulse
- **`velocity`** — Re-rank a subreddit's rising listing by upvotes-per-minute computed from local snapshot deltas, not by Reddit's own opaque rising algorithm.

  _Use this when you need to find posts that broke out in the last hour, not posts that have been quietly accumulating for a day. The 'rising' tab is opaque; this gives you the actual rate._

  ```bash
  reddit-pp-cli velocity wallstreetbets --window 1h --min-score 100 --json
  ```
- **`pulse`** — Compare mentions-per-hour of a topic this window vs the prior window across N subreddits, surfacing which communities are heating up on it.

  _Use this when you want to know which communities are getting agitated about a topic right now compared to last week — the early signal before a story breaks mainstream._

  ```bash
  reddit-pp-cli pulse 'gpt-5' --subs r/ChatGPT,r/MachineLearning,r/OpenAI --vs-window 7d --json
  ```

### Cross-sub aggregation
- **`sweep`** — Top posts mentioning a topic across N subreddits in a window, deduplicated via the local crosspost-edges table so the same viral post counted once.

  _Use this for breaking-story sweeps when you need the highest-signal threads across multiple communities without 80% duplicates from crossposts._

  ```bash
  reddit-pp-cli sweep 'antitrust' --subs r/technology,r/law,r/politics --since 24h --min-score 50 --json --select title,subreddit,score,permalink
  ```
- **`user activity`** — All posts and comments by a user in a window, grouped by subreddit with karma sums, post counts, and first/last activity timestamps per sub.

  _Use this to characterize what someone has been doing across Reddit — researcher use, mod investigation, or just understanding a power user's footprint without scrolling._

  ```bash
  reddit-pp-cli user activity spez --since 30d --group-by sub --json
  ```

### Reddit-specific structure
- **`cascade`** — Recursive walk of a post's crosspost graph rendered as a tree (or `--graph` DOT) showing every downstream crosspost with its subreddit, score, and age.

  _Use this to trace how a viral post propagated across subreddits. Essential for moderation research and any 'where did this story break out' question._

  ```bash
  reddit-pp-cli cascade abc123 --depth 3 --json
  ```
- **`controversial-now`** — Posts in a subreddit window where comment-count divided by score is unusually high — the 'fight in the parking lot' signal that Reddit's own controversial sort fails to surface in the recent window.

  _Use this when you want the spicy current threads in a sub, not the all-time controversial archive. Powers competitive intel and community-temperature reads._

  ```bash
  reddit-pp-cli controversial-now politics --min-ratio 0.5 --since 24h --json
  ```

### Daily driver
- **`watchlist`** — A named bag of subreddits and users; one command refreshes them all and the diff sub-command reports what's new and what's rising since the last sync, grouped by entry.

  _Use this as the daily-driver entrypoint. Replaces the open-12-tabs-and-Ctrl-F ritual every persona does today; agents use it to compute deltas between checks instead of re-fetching._

  ```bash
  reddit-pp-cli watchlist add r/golang && reddit-pp-cli watchlist refresh && reddit-pp-cli watchlist diff --json
  ```

## Usage

Run `reddit-pp-cli --help` for the full command reference and flag list.

## Commands

### comment

Comments by ID

- **`reddit-pp-cli comment get`** - Look up a comment by its t1_ fullname

### discover

Discover subreddits and users

- **`reddit-pp-cli discover subreddit_search`** - Search for subreddits by name or description
- **`reddit-pp-cli discover subreddits`** - Popular subreddits
- **`reddit-pp-cli discover subreddits_new`** - Newly created subreddits
- **`reddit-pp-cli discover users`** - Popular users

### frontpage

Reddit frontpage and best-of

- **`reddit-pp-cli frontpage best`** - Get the Reddit best listing
- **`reddit-pp-cli frontpage get`** - Get the Reddit frontpage (anonymous = best-of; cookie = personalized)

### live

Live threads

- **`reddit-pp-cli live get`** - Live thread updates
- **`reddit-pp-cli live info`** - Live thread metadata: title, description, state, viewer count

### me

Authenticated reads (cookie mode only — run `auth login --chrome` first)

- **`reddit-pp-cli me downvoted`** - Posts the user has downvoted (cookie mode only)
- **`reddit-pp-cli me hidden`** - Posts the user has hidden (cookie mode only)
- **`reddit-pp-cli me saved`** - Posts and comments the user has saved (cookie mode only)
- **`reddit-pp-cli me subscriptions`** - Subreddits the authenticated user is subscribed to (cookie mode only)
- **`reddit-pp-cli me upvoted`** - Posts the user has upvoted (cookie mode only)

### multi

Multireddit feeds

- **`reddit-pp-cli multi get`** - Read a multireddit feed (combined listing across the multi's subs)

### post

Posts and their comment trees

- **`reddit-pp-cli post get`** - Fetch a post and its full comment tree (flattened with depth+score in JSON output)
- **`reddit-pp-cli post info`** - Look up posts by ID(s) without their comment trees

### search_posts

Site-wide post search across all of Reddit

- **`reddit-pp-cli search_posts get`** - Search posts across all subreddits

### subreddit

Subreddit listings, metadata, rules, moderators, wiki, and scoped search

- **`reddit-pp-cli subreddit controversial`** - Controversial listing for a subreddit; --t selects timeframe
- **`reddit-pp-cli subreddit hot`** - Hot listing for a subreddit
- **`reddit-pp-cli subreddit info`** - Subreddit metadata: subscribers, description, created, over_18, type
- **`reddit-pp-cli subreddit moderators`** - Subreddit moderator list
- **`reddit-pp-cli subreddit new`** - New listing for a subreddit
- **`reddit-pp-cli subreddit rising`** - Rising listing for a subreddit (Reddit's own rising algorithm — see also `velocity`)
- **`reddit-pp-cli subreddit rules`** - Subreddit rules
- **`reddit-pp-cli subreddit search`** - Subreddit-scoped search
- **`reddit-pp-cli subreddit top`** - Top listing for a subreddit; --t selects timeframe
- **`reddit-pp-cli subreddit wiki`** - Read a wiki page from a subreddit

### user

User profile, activity, and trophies

- **`reddit-pp-cli user about`** - User metadata: karma, account age, mod status, gold status
- **`reddit-pp-cli user comments`** - Comments by this user
- **`reddit-pp-cli user overview`** - User overview: combined posts and comments in reverse-chrono
- **`reddit-pp-cli user posts`** - Posts submitted by this user
- **`reddit-pp-cli user trophies`** - Trophies awarded to this user


## Output Formats

```bash
# Human-readable table (default in terminal, JSON when piped)
reddit-pp-cli comment mock-value

# JSON for scripting and agents
reddit-pp-cli comment mock-value --json

# Filter to specific fields
reddit-pp-cli comment mock-value --json --select id,name,status

# Dry run — show the request without sending
reddit-pp-cli comment mock-value --dry-run

# Agent mode — JSON + compact + no prompts in one flag
reddit-pp-cli comment mock-value --agent
```

## Agent Usage

This CLI is designed for AI agent consumption:

- **Non-interactive** - never prompts, every input is a flag
- **Pipeable** - `--json` output to stdout, errors to stderr
- **Filterable** - `--select id,name` returns only fields you need
- **Previewable** - `--dry-run` shows the request without sending
- **Read-only by default** - this CLI does not create, update, delete, publish, send, or mutate remote resources
- **Offline-friendly** - sync/search commands can use the local SQLite store when available
- **Agent-safe by default** - no colors or formatting unless `--human-friendly` is set

Exit codes: `0` success, `2` usage error, `3` not found, `4` auth error, `5` API error, `7` rate limited, `10` config error.

## Use with Claude Code

Install the focused skill — it auto-installs the CLI on first invocation:

```bash
npx skills add mvanhorn/printing-press-library/cli-skills/pp-reddit -g
```

Then invoke `/pp-reddit <query>` in Claude Code. The skill is the most efficient path — Claude Code drives the CLI directly without an MCP server in the middle.

<details>
<summary>Use as an MCP server in Claude Code (advanced)</summary>

If you'd rather register this CLI as an MCP server in Claude Code, install the MCP binary first:


Install the MCP binary from this CLI's published public-library entry or pre-built release.

Then register it:

```bash
# Some tools work without auth. For full access, set up auth first:
reddit-pp-cli auth login --chrome

claude mcp add reddit reddit-pp-mcp
```

</details>

## Use with Claude Desktop

This CLI ships an [MCPB](https://github.com/modelcontextprotocol/mcpb) bundle — Claude Desktop's standard format for one-click MCP extension installs (no JSON config required).

The bundle reuses your local browser session — set it up first if you haven't:

```bash
reddit-pp-cli auth login --chrome
```

To install:

1. Download the `.mcpb` for your platform from the [latest release](https://github.com/mvanhorn/printing-press-library/releases/tag/reddit-current).
2. Double-click the `.mcpb` file. Claude Desktop opens and walks you through the install.

Requires Claude Desktop 1.0.0 or later. Pre-built bundles ship for macOS Apple Silicon (`darwin-arm64`) and Windows (`amd64`, `arm64`); for other platforms, use the manual config below.

<details>
<summary>Manual JSON config (advanced)</summary>

If you can't use the MCPB bundle (older Claude Desktop, unsupported platform), install the MCP binary and configure it manually.


Install the MCP binary from this CLI's published public-library entry or pre-built release.

Add to your Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "reddit": {
      "command": "reddit-pp-mcp"
    }
  }
}
```

</details>

## Health Check

```bash
reddit-pp-cli doctor
```

Verifies configuration, credentials, and connectivity to the API.

## Configuration

Config file: `~/.config/reddit-pp-cli/config.toml`

Static request headers can be configured under `headers`; per-command header overrides take precedence.

## Troubleshooting
**Authentication errors (exit code 4)**
- Run `reddit-pp-cli doctor` to check credentials
**Not found errors (exit code 3)**
- Check the resource ID is correct
- Run the `list` command to see available items

### API-specific

- **HTTP 429 with `Retry-After` header on anonymous calls** — The CLI auto-throttles to ≤8 QPM. If you hit 429 anyway, lower with `--throttle 5` or run `auth login --chrome` to lift the cap to ~80 QPM.
- **HTTP 403 with HTML body on anonymous calls** — Reddit blocks default User-Agents. Verify `doctor` shows the polite UA. If you customized it, follow Reddit's convention: `<api>:vX.Y.Z (by /u/<your-handle>)`.
- **`auth login --chrome` returns 'no session cookie found'** — Log in to reddit.com in your default Chrome profile first, then re-run. The CLI reads the `reddit_session` cookie from Chrome's local profile DB.
- **`me subscriptions` returns empty** — Cookie-only command — run `auth login --chrome` first. `doctor` will show 'cookie mode: inactive' if the cookie is missing or expired.
- **`velocity` returns 'insufficient snapshots'** — Run `sync` for the subreddit at least twice with at least `--window` apart. Velocity needs two data points to compute a rate.
- **Comment tree truncated with 'more children' placeholders** — Pass `--expand` to `post <id>` to walk the continuation pointers. Costs more API calls but flattens the full tree.

---

## Sources & Inspiration

This CLI was built by studying these projects and resources:

- [**PRAW**](https://github.com/praw-dev/praw) — Python (3600 stars)
- [**snoowrap (archived)**](https://github.com/not-an-aardvark/snoowrap) — JavaScript (1000 stars)
- [**JRAW**](https://github.com/mattbdean/JRAW) — Java (570 stars)
- [**redditwarp**](https://github.com/Pyprohly/redditwarp) — Python (70 stars)
- [**Hawstein/mcp-server-reddit**](https://github.com/Hawstein/mcp-server-reddit) — Python
- [**jordanburke/reddit-mcp-server**](https://github.com/jordanburke/reddit-mcp-server) — TypeScript
- [**eliasbiondo/reddit-mcp-server**](https://github.com/eliasbiondo/reddit-mcp-server) — TypeScript
- [**rtv-plus**](https://github.com/dhrm1k/rtv-plus) — Python

Generated by [CLI Printing Press](https://github.com/mvanhorn/cli-printing-press)
