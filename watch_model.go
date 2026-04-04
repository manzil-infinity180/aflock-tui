package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type watchView int

const (
	watchViewTable  watchView = iota
	watchViewDetail
)

// watchTickMsg signals that the ticker fired.
type watchTickMsg time.Time

// watchNewResultsMsg carries newly evaluated results from a poll.
type watchNewResultsMsg struct {
	results []ReplayResult
	session ReplaySession
}

// watchErrorMsg carries an error from polling.
type watchErrorMsg struct{ err error }

// watchInitDone signals the initial watch state was created.
type watchInitDone struct {
	ws  *watchState
	err error
}

type watchModel struct {
	sessionPath string
	policyPath  string
	aflockBin   string

	ws         *watchState
	results    []ReplayResult
	policyName string
	session    ReplaySession
	err        error
	loading    bool

	allowCount int
	denyCount  int
	askCount   int

	view     watchView
	cursor   int
	scroll   int
	viewport viewport.Model
	width    int
	height   int
	ready    bool

	blinkOn bool // for the LIVE indicator
}

func newWatchModel(sessionPath, policyPath, aflockBin string) watchModel {
	return watchModel{
		sessionPath: sessionPath,
		policyPath:  policyPath,
		aflockBin:   aflockBin,
		loading:     true,
		blinkOn:     true,
	}
}

func (m watchModel) Init() tea.Cmd {
	return func() tea.Msg {
		ws, err := newWatchState(m.sessionPath, m.policyPath, m.aflockBin)
		return watchInitDone{ws, err}
	}
}

func watchTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return watchTickMsg(t)
	})
}

func (m watchModel) pollCmd() tea.Cmd {
	return func() tea.Msg {
		if m.ws == nil {
			return watchErrorMsg{fmt.Errorf("watch state not initialized")}
		}
		results, err := m.ws.watchPollAndEvaluate()
		if err != nil {
			return watchErrorMsg{err}
		}
		return watchNewResultsMsg{
			results: results,
			session: m.ws.session,
		}
	}
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case watchInitDone:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.ws = msg.ws
		m.policyName = msg.ws.policyName()
		// Do initial poll + start ticker
		return m, tea.Batch(m.pollCmd(), watchTickCmd())

	case watchTickMsg:
		m.blinkOn = !m.blinkOn
		return m, tea.Batch(m.pollCmd(), watchTickCmd())

	case watchNewResultsMsg:
		if len(msg.results) > 0 {
			m.results = append(m.results, msg.results...)
			m.session = msg.session

			for _, r := range msg.results {
				switch r.Decision {
				case "ALLOW":
					m.allowCount++
				case "DENY":
					m.denyCount++
				case "ASK":
					m.askCount++
				}
			}

			// Auto-scroll to latest in table view
			if m.view == watchViewTable {
				m.cursor = len(m.results) - 1
				maxVis := m.height - 14
				if maxVis < 1 {
					maxVis = 10
				}
				if m.cursor >= m.scroll+maxVis {
					m.scroll = m.cursor - maxVis + 1
				}
			}
		} else if msg.session.Model != "" {
			m.session = msg.session
		}
		return m, nil

	case watchErrorMsg:
		m.err = msg.err
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-3)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - 3
		}
		m.updateWatchViewport()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.ws != nil {
				m.ws.cleanup()
			}
			return m, tea.Quit

		case "esc":
			if m.view == watchViewDetail {
				m.view = watchViewTable
				return m, nil
			}
			if m.ws != nil {
				m.ws.cleanup()
			}
			return m, tea.Quit

		case "enter":
			if m.view == watchViewTable && m.cursor < len(m.results) {
				m.view = watchViewDetail
				m.updateWatchViewport()
			}
			return m, nil

		case "up", "k":
			if m.view == watchViewTable {
				if m.cursor > 0 {
					m.cursor--
					if m.cursor < m.scroll {
						m.scroll = m.cursor
					}
				}
			} else {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyUp})
				return m, cmd
			}
			return m, nil

		case "down", "j":
			if m.view == watchViewTable {
				if m.cursor < len(m.results)-1 {
					m.cursor++
					maxVis := m.height - 14
					if maxVis < 1 {
						maxVis = 10
					}
					if m.cursor >= m.scroll+maxVis {
						m.scroll = m.cursor - maxVis + 1
					}
				}
			} else {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyDown})
				return m, cmd
			}
			return m, nil
		}

		// Forward scrolling in detail view
		if m.view == watchViewDetail {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.view == watchViewTable {
				if m.cursor > 0 {
					m.cursor--
					if m.cursor < m.scroll {
						m.scroll = m.cursor
					}
				}
			} else {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		case tea.MouseButtonWheelDown:
			if m.view == watchViewTable {
				if m.cursor < len(m.results)-1 {
					m.cursor++
					maxVis := m.height - 14
					if maxVis < 1 {
						maxVis = 10
					}
					if m.cursor >= m.scroll+maxVis {
						m.scroll = m.cursor - maxVis + 1
					}
				}
			} else {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

func (m *watchModel) updateWatchViewport() {
	if !m.ready || m.view != watchViewDetail {
		return
	}
	if m.cursor >= len(m.results) {
		return
	}
	m.viewport.SetContent(m.renderWatchDetail())
	m.viewport.GotoTop()
}

func (m watchModel) View() string {
	if m.loading {
		return "\n  Starting watch mode...\n"
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.", m.err)
	}

	switch m.view {
	case watchViewDetail:
		header := titleStyle.Render(fmt.Sprintf(" Action #%d: %s ",
			m.results[m.cursor].Action.Index,
			m.results[m.cursor].Action.Tool))
		footer := statusBarStyle.Render(" esc:back  j/k:scroll  q:quit")
		return header + "\n\n" + m.viewport.View() + "\n" + footer
	default:
		return m.renderWatchTable()
	}
}

func (m watchModel) renderWatchTable() string {
	var b strings.Builder

	// Header with LIVE indicator
	liveIndicator := ""
	if m.blinkOn {
		liveIndicator = redStyle.Render(" \u25cf LIVE ")
	} else {
		liveIndicator = dimStyle.Render(" \u25cb LIVE ")
	}
	b.WriteString(titleStyle.Render(" Watch Mode ") + "  " + liveIndicator + "\n\n")

	// Session info
	sessionFile := filepath.Base(m.sessionPath)
	policyFile := filepath.Base(m.policyPath)
	b.WriteString(kv("Session", dimStyle.Render(sessionFile)))
	b.WriteString(kv("Policy", subtitleStyle.Render(m.policyName)+" "+dimStyle.Render(policyFile)))
	if m.session.Model != "" {
		b.WriteString(kv("Model", cyanStyle.Render(m.session.Model)))
	}
	b.WriteString(kv("Stats", fmt.Sprintf("%s turns  %s calls  in:%s out:%s",
		cyanStyle.Render(fmt.Sprintf("%d", m.session.Turns)),
		cyanStyle.Render(fmt.Sprintf("%d", m.session.ToolCalls)),
		dimStyle.Render(fmt.Sprintf("%d", m.session.TokensIn)),
		dimStyle.Render(fmt.Sprintf("%d", m.session.TokensOut)))))
	b.WriteString(kv("Result",
		greenStyle.Render(fmt.Sprintf("%d allow", m.allowCount))+"  "+
			redStyle.Render(fmt.Sprintf("%d deny", m.denyCount))+"  "+
			yellowStyle.Render(fmt.Sprintf("%d ask", m.askCount))))
	b.WriteString("\n")

	// Table header
	hdr := fmt.Sprintf("  %-4s  %-10s  %-8s  %s", "#", "TOOL", "DECISION", "INPUT")
	b.WriteString(dimStyle.Render(hdr) + "\n")
	lw := min(m.width-4, 100)
	if lw < 1 {
		lw = 60
	}
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", lw)) + "\n")

	// Actions
	maxVis := m.height - 14
	if maxVis < 1 {
		maxVis = 10
	}

	if len(m.results) == 0 {
		b.WriteString(dimStyle.Render("  Waiting for tool calls...") + "\n")
	}

	for i := m.scroll; i < len(m.results) && i < m.scroll+maxVis; i++ {
		res := m.results[i]

		var decStyle func(string) string
		switch res.Decision {
		case "ALLOW":
			decStyle = func(s string) string { return greenStyle.Render(s) }
		case "DENY":
			decStyle = func(s string) string { return redStyle.Render(s) }
		case "ASK":
			decStyle = func(s string) string { return yellowStyle.Render(s) }
		default:
			decStyle = func(s string) string { return dimStyle.Render(s) }
		}

		detail := res.Action.Detail
		policyDir := filepath.Dir(m.policyPath)
		if absDir, err := filepath.Abs(policyDir); err == nil {
			detail = strings.TrimPrefix(detail, absDir+"/")
		}
		if len(detail) > 55 {
			detail = detail[:55] + "..."
		}

		num := fmt.Sprintf("%d", res.Action.Index)
		line := fmt.Sprintf("%-4s  %-10s  %-8s  %s",
			dimStyle.Render(num),
			cyanStyle.Render(res.Action.Tool),
			decStyle(fmt.Sprintf("%-6s", res.Decision)),
			detail)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(" > "+line) + "\n")
		} else {
			b.WriteString(normalStyle.Render("   "+line) + "\n")
		}
	}

	b.WriteString("\n")

	// Preview of selected action
	if m.cursor < len(m.results) {
		res := m.results[m.cursor]
		sep := dimStyle.Render("  " + strings.Repeat("─", lw))
		b.WriteString(sep + "\n")

		detail := res.Action.Detail
		reason := ""
		if res.Reason != "" {
			reason = strings.TrimPrefix(res.Reason, "[aflock] BLOCKED: ")
		}

		preview := fmt.Sprintf("  %s %s  %s",
			cyanStyle.Render(res.Action.Tool),
			dimStyle.Render(detail),
			"")
		if reason != "" {
			preview += redStyle.Render(reason)
		}
		b.WriteString(preview + "\n")
	}

	b.WriteString("\n")
	footer := statusBarStyle.Render(" enter:detail  j/k:navigate  q:quit")
	b.WriteString(footer)

	return b.String()
}

func (m watchModel) renderWatchDetail() string {
	if m.cursor >= len(m.results) {
		return ""
	}

	res := m.results[m.cursor]
	var b strings.Builder
	sep := dimStyle.Render("  " + strings.Repeat("─", clampLineW(m.width)))

	// Decision
	b.WriteString(section("DECISION", ""))
	switch res.Decision {
	case "ALLOW":
		b.WriteString(kv("Result", greenStyle.Render("  ALLOW  ")))
	case "DENY":
		b.WriteString(kv("Result", redStyle.Render("  DENY   ")))
		if res.Reason != "" {
			reason := strings.TrimPrefix(res.Reason, "[aflock] BLOCKED: ")
			b.WriteString(kv("Reason", redStyle.Render(reason)))
		}
	case "ASK":
		b.WriteString(kv("Result", yellowStyle.Render("  ASK    ")))
		if res.Reason != "" {
			b.WriteString(kv("Reason", yellowStyle.Render(res.Reason)))
		}
	}

	// Action details
	b.WriteString("\n" + sep + "\n")
	b.WriteString(section("ACTION", "Tool Call"))
	b.WriteString(kv("Tool", cyanStyle.Render(res.Action.Tool)))
	b.WriteString(kv("ID", dimStyle.Render(res.Action.ID)))

	// Input fields
	for k, v := range res.Action.Input {
		val := fmt.Sprintf("%v", v)
		if len(val) > 80 {
			val = val[:80] + "..."
		}
		b.WriteString(kv("  "+k, jsonStrStyle.Render(val)))
	}

	// Policy context
	b.WriteString("\n" + sep + "\n")
	b.WriteString(section("POLICY", "Context"))
	b.WriteString(kv("Policy", subtitleStyle.Render(m.policyName)))
	absPolicy, _ := filepath.Abs(m.policyPath)
	b.WriteString(kv("Path", dimStyle.Render(absPolicy)))

	return b.String()
}
