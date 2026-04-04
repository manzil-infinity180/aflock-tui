# aflock-tui

Terminal UI for inspecting [aflock](https://github.com/aflock-ai/aflock) sessions — browse sessions, decode attestations, view JWTs, replay sessions against policies, and watch live agent sessions.

Built with Go + [Bubbletea](https://github.com/charmbracelet/bubbletea).

## Install

```bash
go install github.com/aflock-ai/aflock-tui@latest
```

Or build from source:

```bash
git clone https://github.com/aflock-ai/aflock-tui.git
cd aflock-tui
go build -o aflock-tui .
```

## Usage

### Session Browser (default)

```bash
aflock-tui
```

Browse all aflock sessions from `~/.aflock/sessions/`. Navigate with `j/k`, press `Enter` to inspect.

**What you can see:**
- **Inspect tab** — WHO (model, binary, identity hash), WHAT (tool calls, decisions), WHEN (timestamps, cost, tokens), PROOF (attestation count, JWT)
- **Actions tab** — chronological list of every policy decision (ALLOW/DENY) with reasons
- **Policy tab** — the full policy that governed the session
- **State tab** — raw `state.json`
- **Attestations** — decode DSSE envelopes, view in-toto statements, raw payloads
- **JWT** — decoded header, claims, SPIFFE ID, expiry

### Replay

```bash
aflock-tui replay <session.jsonl> <policy.aflock>
```

Replay a recorded Claude Code session against any aflock policy. Each tool call is evaluated and shown with ALLOW/DENY/ASK decisions.

Uses `aflock replay --format json` under the hood (single subprocess call), falling back to per-action hook evaluation if the CLI doesn't support replay.

Press `s` for the summary dashboard — tool breakdown, decision timeline, deny list.

### Watch (Live Tail)

```bash
aflock-tui watch <session.jsonl> <policy.aflock>
```

Live-tail a Claude Code session as the agent runs. New tool calls appear in real-time with policy decisions. Useful for:
- Policy development — see if your policy is too strict or too permissive as the agent works
- Debugging — see exactly which tool call got blocked and why
- Demos — show policy enforcement happening in real-time

Run this in one terminal, Claude Code in another. The TUI polls the `.jsonl` file every 500ms and evaluates new actions.

## Keyboard Shortcuts

### Session Browser

| Key | Action |
|-----|--------|
| `j/k` or arrows | Navigate sessions |
| `Enter` | Inspect selected session |
| `Tab` | Switch tabs (Inspect/Actions/Policy/State) |
| `a` | View attestations |
| `t` | View JWT token |
| `/` | Filter sessions |
| `c` | Copy content to clipboard |
| `p` | Copy path to clipboard |
| `o` | Open in Finder |
| `d` | Delete session |
| `q` | Quit |

### Replay

| Key | Action |
|-----|--------|
| `j/k` or arrows | Navigate actions |
| `Enter` | Expand action details |
| `s` | Summary dashboard |
| `c` | Copy report to clipboard |
| `Esc` | Back to table / quit |
| `q` | Quit |

### Watch

| Key | Action |
|-----|--------|
| `j/k` or arrows | Navigate actions |
| `Enter` | Expand action details |
| `Esc` | Back to table |
| `q` | Quit |

## Requirements

- **Go 1.23+** to build
- **aflock** binary in PATH or at a known location (for replay/watch modes)
  ```bash
  # Build aflock
  cd /path/to/aflock
  go build -o /usr/local/bin/aflock ./cmd/aflock/
  ```

## Finding Session Files

```bash
# aflock sessions (for the browser)
ls ~/.aflock/sessions/

# Claude Code sessions (for replay/watch)
ls -lt ~/.claude/projects/*/*.jsonl | head -5
```

## Examples

```bash
# Browse sessions
aflock-tui

# Replay a session against a strict policy
aflock-tui replay ~/.claude/projects/my-project/abc123.jsonl ./strict.aflock

# Watch a live session
# Terminal 1: start Claude Code in your project
# Terminal 2:
aflock-tui watch $(ls -t ~/.claude/projects/my-project/*.jsonl | head -1) .aflock
```

## Related

- [aflock](https://github.com/aflock-ai/aflock) — Cryptographically signed policies for constrained AI agent execution
- [aflock-replay](https://github.com/manzil-infinity180/aflock-replay) — Browser-based replay UI (WebAssembly)

## License

MIT
