package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

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

	// Usage: aflock-tui watch <session.jsonl> <policy.aflock>
	if len(os.Args) >= 4 && os.Args[1] == "watch" {
		sessionPath := os.Args[2]
		policyPath := os.Args[3]

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

	if len(os.Args) >= 2 && os.Args[1] == "watch" {
		fmt.Fprintln(os.Stderr, "Usage: aflock-tui watch <session.jsonl> <policy.aflock>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  aflock-tui watch ~/.claude/projects/.../<uuid>.jsonl ./path/to/.aflock")
		os.Exit(1)
	}

	m := newModel()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
