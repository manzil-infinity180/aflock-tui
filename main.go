package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
)

// findLatestSession finds the most recently modified .jsonl file in
// ~/.claude/projects/. Returns the path or "" if none found.
func findLatestSession() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	pattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}

	// Sort by modification time, newest first
	sort.Slice(matches, func(i, j int) bool {
		infoI, errI := os.Stat(matches[i])
		infoJ, errJ := os.Stat(matches[j])
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	return matches[0]
}

// findRecentSessions returns the N most recently modified .jsonl files.
func findRecentSessions(n int) []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	pattern := filepath.Join(homeDir, ".claude", "projects", "*", "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}

	sort.Slice(matches, func(i, j int) bool {
		infoI, errI := os.Stat(matches[i])
		infoJ, errJ := os.Stat(matches[j])
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	if n > len(matches) {
		n = len(matches)
	}
	return matches[:n]
}

func main() {
	// Usage: aflock-tui replay <session.jsonl> <policy.aflock>
	if len(os.Args) >= 4 && os.Args[1] == "replay" {
		sessionPath := os.Args[2]
		policyPath := os.Args[3]

		aflockBin := findAflockBin()
		if aflockBin == "" {
			fmt.Fprintln(os.Stderr, "Error: aflock binary not found. Set it in PATH or at /Users/rahulxf/work-dir/aflock/bin/aflock")
			os.Exit(1)
		}

		m := newReplayModel(sessionPath, policyPath, aflockBin)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) >= 2 && os.Args[1] == "replay" {
		fmt.Fprintln(os.Stderr, "Usage: aflock-tui replay <session.jsonl> <policy.aflock>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  aflock-tui replay ~/.claude/projects/.../<uuid>.jsonl ./path/to/.aflock")
		os.Exit(1)
	}

	// Usage: aflock-tui watch [--live] <policy.aflock>
	//        aflock-tui watch <session.jsonl> <policy.aflock>
	if len(os.Args) >= 2 && os.Args[1] == "watch" {
		var sessionPath, policyPath string

		if len(os.Args) >= 4 && os.Args[2] == "--live" {
			// --live mode: auto-discover latest Claude session
			policyPath = os.Args[3]
			sessionPath = findLatestSession()
			if sessionPath == "" {
				fmt.Fprintln(os.Stderr, "Error: no Claude sessions found in ~/.claude/projects/")
				fmt.Fprintln(os.Stderr, "Start Claude Code in a project first, then run watch --live.")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "Watching latest session: %s\n", filepath.Base(sessionPath))
		} else if len(os.Args) >= 4 {
			sessionPath = os.Args[2]
			policyPath = os.Args[3]
		} else if len(os.Args) == 3 {
			// Single arg after watch — could be --live with no policy, or just policy
			if os.Args[2] == "--live" {
				// --live without policy — try to find .aflock in cwd
				policyPath = ".aflock"
				if _, err := os.Stat(policyPath); err != nil {
					fmt.Fprintln(os.Stderr, "Usage: aflock-tui watch --live <policy.aflock>")
					fmt.Fprintln(os.Stderr, "  or place a .aflock file in the current directory")
					os.Exit(1)
				}
				sessionPath = findLatestSession()
				if sessionPath == "" {
					fmt.Fprintln(os.Stderr, "Error: no Claude sessions found in ~/.claude/projects/")
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "Watching latest session: %s\n", filepath.Base(sessionPath))
			} else {
				fmt.Fprintln(os.Stderr, "Usage: aflock-tui watch <session.jsonl> <policy.aflock>")
				fmt.Fprintln(os.Stderr, "       aflock-tui watch --live <policy.aflock>")
				fmt.Fprintln(os.Stderr, "       aflock-tui watch --live   (uses .aflock in cwd)")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "  --live  auto-discovers the most recent Claude session")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Recent sessions:")
				for i, s := range findRecentSessions(5) {
					info, _ := os.Stat(s)
					mod := ""
					if info != nil {
						mod = info.ModTime().Format("Jan 2 15:04")
					}
					fmt.Fprintf(os.Stderr, "  %d. %s  %s\n", i+1, mod, filepath.Base(s))
				}
				os.Exit(1)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Usage: aflock-tui watch <session.jsonl> <policy.aflock>")
			fmt.Fprintln(os.Stderr, "       aflock-tui watch --live <policy.aflock>")
			fmt.Fprintln(os.Stderr, "       aflock-tui watch --live   (uses .aflock in cwd)")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  --live  auto-discovers the most recent Claude session")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Recent sessions:")
			for i, s := range findRecentSessions(5) {
				info, _ := os.Stat(s)
				mod := ""
				if info != nil {
					mod = info.ModTime().Format("Jan 2 15:04")
				}
				fmt.Fprintf(os.Stderr, "  %d. %s  %s\n", i+1, mod, filepath.Base(s))
			}
			os.Exit(1)
		}

		aflockBin := findAflockBin()
		if aflockBin == "" {
			fmt.Fprintln(os.Stderr, "Error: aflock binary not found. Set it in PATH or at /Users/rahulxf/work-dir/aflock/bin/aflock")
			os.Exit(1)
		}

		m := newWatchModel(sessionPath, policyPath, aflockBin)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	m := newModel()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
