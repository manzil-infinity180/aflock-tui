package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// verifyResult is the message Bubbletea passes back when the verify chain
// finishes. It carries the rendered text for the verify viewport.
type verifyResult struct {
	output  string
	elapsed time.Duration
}

// findAflockBinary locates the aflock CLI to run `aflock verify`.
//
// Resolution order:
//  1. AFLOCK_BIN env var (explicit override)
//  2. `aflock` on PATH
//  3. Well-known locations under $HOME (peer-cred-pr88 sandbox, ~/go/bin)
//
// Returns "" if nothing is found — caller should surface a friendly error
// pointing the user at the env var.
func findAflockBinary() string {
	if v := os.Getenv("AFLOCK_BIN"); v != "" {
		if fi, err := os.Stat(v); err == nil && !fi.IsDir() {
			return v
		}
	}
	if p, err := exec.LookPath("aflock"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "work-dir", "aflock-example", "peer-cred-pr88", "bin", "aflock"),
		filepath.Join(home, "go", "bin", "aflock"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// latestAttestationKeyID extracts the SPIFFE keyid from the most recent
// attestation in sessionDir. Empty string if no attestation exists or it
// can't be parsed. Used for the headline "signing identity:" line.
func latestAttestationKeyID(sessionDir string) string {
	attestDir := filepath.Join(sessionDir, "attestations")
	entries, err := os.ReadDir(attestDir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".intoto.json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(newestTime) {
			newestTime = fi.ModTime()
			newest = filepath.Join(attestDir, e.Name())
		}
	}
	if newest == "" {
		return ""
	}
	data, err := os.ReadFile(newest) //nolint:gosec // path constructed from session dir
	if err != nil {
		return ""
	}
	var env struct {
		Signatures []struct {
			KeyID string `json:"keyid"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}
	if len(env.Signatures) == 0 {
		return ""
	}
	return env.Signatures[0].KeyID
}

// runVerifyCmd builds a tea.Cmd that runs `aflock verify --session <id>`
// out-of-process and returns the formatted output as a verifyResult msg.
//
// The output is rendered as plain text (with section headers) suitable for
// the viewport. We deliberately don't try to colorize per-check pass/fail
// here — the existing TUI styles cover that on render.
func runVerifyCmd(sessionID, sessionDir string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		var b strings.Builder

		fmt.Fprintf(&b, "session: %s\n", sessionID)
		if keyID := latestAttestationKeyID(sessionDir); keyID != "" {
			fmt.Fprintf(&b, "signing identity (latest attestation):\n  %s\n", keyID)
			if strings.Contains(keyID, "/ephemeral/") {
				b.WriteString("  ⚠  ephemeral signing tier — SPIRE not active for this session\n")
			} else if strings.HasPrefix(keyID, "spiffe://") {
				b.WriteString("  ✓  SPIRE-rooted (chain verifiable against SPIRE CA bundle)\n")
			}
		} else {
			b.WriteString("signing identity: (no attestations on disk yet)\n")
		}
		b.WriteString("\n──────────────────────────────────────────────\n\n")

		bin := findAflockBinary()
		if bin == "" {
			b.WriteString("aflock binary not found.\n")
			b.WriteString("Set AFLOCK_BIN=/path/to/aflock or put it on PATH.\n")
			return verifyResult{output: b.String(), elapsed: time.Since(start)}
		}
		fmt.Fprintf(&b, "aflock: %s\n\n", bin)

		cmd := exec.Command(bin, "verify", "--session", sessionID) //nolint:gosec // path validated above
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(&b, "verify command failed: %v\n\n", err)
		}
		// Pretty-print the JSON if it parses, otherwise emit raw.
		var pretty interface{}
		if json.Unmarshal(out, &pretty) == nil {
			pp, _ := json.MarshalIndent(pretty, "", "  ")
			b.Write(pp)
			b.WriteString("\n")
		} else {
			b.Write(out)
		}

		// Decode each check into a quick summary at the top of the buffer.
		// We re-parse out specifically to extract the checks array.
		var verifyJSON struct {
			Success bool `json:"success"`
			Checks  []struct {
				Name    string `json:"name"`
				Passed  bool   `json:"passed"`
				Message string `json:"message"`
			} `json:"checks"`
		}
		if json.Unmarshal(out, &verifyJSON) == nil && len(verifyJSON.Checks) > 0 {
			var summary strings.Builder
			summary.WriteString("\n──────────────────────────────────────────────\n")
			fmt.Fprintf(&summary, "summary: %d checks — %s\n",
				len(verifyJSON.Checks),
				map[bool]string{true: "ALL PASS", false: "FAIL"}[verifyJSON.Success])
			for _, c := range verifyJSON.Checks {
				marker := "✗"
				if c.Passed {
					marker = "✓"
				}
				line := fmt.Sprintf("  %s %s", marker, c.Name)
				if !c.Passed && c.Message != "" {
					trimmed := strings.ReplaceAll(c.Message, "\n", " ")
					if len(trimmed) > 90 {
						trimmed = trimmed[:90] + "…"
					}
					line += "  — " + trimmed
				}
				summary.WriteString(line + "\n")
			}
			b.WriteString(summary.String())
		}

		return verifyResult{output: b.String(), elapsed: time.Since(start)}
	}
}

// decodeAttestationPredicate is a small helper: given an attestation envelope
// path, returns the decoded predicate JSON (pretty-printed). Used by the
// verify view's optional "show first attestation predicate" footer.
func decodeAttestationPredicate(envelopePath string) (string, error) {
	data, err := os.ReadFile(envelopePath) //nolint:gosec // path is from session dir
	if err != nil {
		return "", err
	}
	var env struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", err
	}
	body, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return "", err
	}
	var stmt struct {
		Predicate json.RawMessage `json:"predicate"`
	}
	if err := json.Unmarshal(body, &stmt); err != nil {
		return "", err
	}
	var pretty interface{}
	if err := json.Unmarshal(stmt.Predicate, &pretty); err != nil {
		return string(stmt.Predicate), nil
	}
	pp, _ := json.MarshalIndent(pretty, "", "  ")
	return string(pp), nil
}
