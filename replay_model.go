package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type replayView int

const (
	replayViewTable   replayView = iota // action table
	replayViewDetail                     // selected action detail
	replayViewSummary                    // summary dashboard
)

type replayModel struct {
	sessionPath string
	policyPath  string
	aflockBin   string
	report      *ReplayReport
	err         error
	loading     bool
	useCLI      bool

	view     replayView
	cursor   int
	scroll   int
	viewport viewport.Model
	width    int
	height   int
	ready    bool
	copied   string
}

type replayDone struct {
	report *ReplayReport
	err    error
}

func newReplayModel(sessionPath, policyPath, aflockBin string) replayModel {
	return replayModel{
		sessionPath: sessionPath,
		policyPath:  policyPath,
		aflockBin:   aflockBin,
		loading:     true,
		useCLI:      replayCLISupported(aflockBin),
	}
}

func (m replayModel) Init() tea.Cmd {
	return func() tea.Msg {
		// Try the single-call CLI replay first
		if m.useCLI {
			report, err := runReplayCLI(m.sessionPath, m.policyPath, m.aflockBin)
			if err == nil {
				return replayDone{report, nil}
			}
			// Fall back to per-action replay on error
		}
		session, err := parseSessionFile(m.sessionPath)
		if err != nil {
			return replayDone{nil, err}
		}
		report, err := runReplay(session, m.policyPath, m.aflockBin)
		return replayDone{report, err}
	}
}

func (m replayModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case replayDone:
		m.loading = false
		m.report = msg.report
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
		m.updateReplayViewport()
		return m, nil

	case tea.KeyMsg:
		m.copied = ""

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "esc":
			if m.view == replayViewDetail || m.view == replayViewSummary {
				m.view = replayViewTable
				return m, nil
			}
			return m, tea.Quit

		case "s":
			if m.view == replayViewTable && m.report != nil {
				m.view = replayViewSummary
				m.updateReplayViewport()
				return m, nil
			}
			return m, nil

		case "enter":
			if m.view == replayViewTable && m.report != nil && m.cursor < len(m.report.Results) {
				m.view = replayViewDetail
				m.updateReplayViewport()
			}
			return m, nil

		case "up", "k":
			if m.view == replayViewTable {
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
			if m.view == replayViewTable {
				if m.report != nil && m.cursor < len(m.report.Results)-1 {
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

		case "c":
			if m.report != nil {
				content := m.reportToText()
				return m.copyReplay(content, "report")
			}
			return m, nil
		}

		// Forward scrolling in detail/summary view
		if m.view == replayViewDetail || m.view == replayViewSummary {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.view == replayViewTable {
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
			if m.view == replayViewTable {
				if m.report != nil && m.cursor < len(m.report.Results)-1 {
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

func (m *replayModel) updateReplayViewport() {
	if !m.ready {
		return
	}
	if m.report == nil {
		return
	}
	switch m.view {
	case replayViewDetail:
		if m.cursor >= len(m.report.Results) {
			return
		}
		m.viewport.SetContent(m.renderActionDetail())
		m.viewport.GotoTop()
	case replayViewSummary:
		m.viewport.SetContent(m.renderSummaryDashboard())
		m.viewport.GotoTop()
	}
}

func (m *replayModel) copyReplay(content, label string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	cmd := pbcopy(content)
	if cmd != nil {
		m.copied = "copy failed"
	} else {
		m.copied = label + " copied"
	}
	return m, nil
}

func pbcopy(content string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func (m replayModel) View() string {
	if m.loading {
		return "\n  Replaying session against policy...\n\n  Evaluating each tool call through aflock's policy engine.\n"
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.", m.err)
	}

	if m.report == nil {
		return "\n  No report generated.\n"
	}

	switch m.view {
	case replayViewDetail:
		header := titleStyle.Render(fmt.Sprintf(" Action #%d: %s ",
			m.report.Results[m.cursor].Action.Index,
			m.report.Results[m.cursor].Action.Tool))
		footer := statusBarStyle.Render(" esc:back  j/k:scroll  q:quit")
		if m.copied != "" {
			footer = copiedStyle.Render(" "+m.copied+" ") + "  " + footer
		}
		return header + "\n\n" + m.viewport.View() + "\n" + footer
	case replayViewSummary:
		header := titleStyle.Render(" Summary Dashboard ")
		footer := statusBarStyle.Render(" esc:back  j/k:scroll  q:quit")
		if m.copied != "" {
			footer = copiedStyle.Render(" "+m.copied+" ") + "  " + footer
		}
		return header + "\n\n" + m.viewport.View() + "\n" + footer
	default:
		return m.renderReplayTable()
	}
}

func (m replayModel) renderReplayTable() string {
	r := m.report
	var b strings.Builder

	// Header
	verdict := greenStyle.Render(" PASS ")
	if r.DenyCount > 0 {
		verdict = redStyle.Render(" FAIL ")
	}
	b.WriteString(titleStyle.Render(" Replay Validator ") + "  " + verdict + "\n\n")

	// Session info
	sessionFile := filepath.Base(m.sessionPath)
	policyFile := filepath.Base(m.policyPath)
	b.WriteString(kv("Session", dimStyle.Render(sessionFile)))
	b.WriteString(kv("Policy", subtitleStyle.Render(r.PolicyName)+" "+dimStyle.Render(policyFile)))
	b.WriteString(kv("Model", cyanStyle.Render(r.Session.Model)))
	b.WriteString(kv("Stats", fmt.Sprintf("%s turns  %s calls  in:%s out:%s",
		cyanStyle.Render(fmt.Sprintf("%d", r.Session.Turns)),
		cyanStyle.Render(fmt.Sprintf("%d", r.Session.ToolCalls)),
		dimStyle.Render(fmt.Sprintf("%d", r.Session.TokensIn)),
		dimStyle.Render(fmt.Sprintf("%d", r.Session.TokensOut)))))
	b.WriteString(kv("Result",
		greenStyle.Render(fmt.Sprintf("%d allow", r.AllowCount))+"  "+
			redStyle.Render(fmt.Sprintf("%d deny", r.DenyCount))+"  "+
			yellowStyle.Render(fmt.Sprintf("%d ask", r.AskCount))))
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

	for i := m.scroll; i < len(r.Results) && i < m.scroll+maxVis; i++ {
		res := r.Results[i]

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

		// Shorten detail relative to policy dir
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
	if m.cursor < len(r.Results) {
		res := r.Results[m.cursor]
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
	footer := statusBarStyle.Render(" enter:detail  s:summary  c:copy report  j/k:navigate  q:quit")
	if m.copied != "" {
		footer = copiedStyle.Render(" "+m.copied+" ") + "  " + footer
	}
	b.WriteString(footer)

	return b.String()
}

func (m replayModel) renderActionDetail() string {
	if m.report == nil || m.cursor >= len(m.report.Results) {
		return ""
	}

	res := m.report.Results[m.cursor]
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
	b.WriteString(kv("Policy", subtitleStyle.Render(m.report.PolicyName)))
	b.WriteString(kv("Path", dimStyle.Render(m.report.PolicyPath)))

	return b.String()
}

// reportToText generates a plain-text version of the replay report for copying.
func (m replayModel) reportToText() string {
	if m.report == nil {
		return ""
	}

	r := m.report
	var b strings.Builder

	b.WriteString(fmt.Sprintf("AFLOCK REPLAY REPORT\n"))
	b.WriteString(fmt.Sprintf("Session: %s\n", m.sessionPath))
	b.WriteString(fmt.Sprintf("Policy:  %s (%s)\n", r.PolicyName, m.policyPath))
	b.WriteString(fmt.Sprintf("Model:   %s\n", r.Session.Model))
	b.WriteString(fmt.Sprintf("Turns:   %d  Tool Calls: %d\n", r.Session.Turns, r.Session.ToolCalls))
	b.WriteString(fmt.Sprintf("Tokens:  in=%d out=%d\n", r.Session.TokensIn, r.Session.TokensOut))
	b.WriteString(fmt.Sprintf("Result:  %d allow, %d deny, %d ask\n", r.AllowCount, r.DenyCount, r.AskCount))

	verdict := "PASS"
	if r.DenyCount > 0 {
		verdict = fmt.Sprintf("FAIL — %d violation(s)", r.DenyCount)
	}
	b.WriteString(fmt.Sprintf("Verdict: %s\n\n", verdict))

	b.WriteString(fmt.Sprintf("%-4s  %-12s  %-8s  %s\n", "#", "TOOL", "DECISION", "INPUT"))
	b.WriteString(strings.Repeat("─", 90) + "\n")

	for _, res := range r.Results {
		detail := res.Action.Detail
		if len(detail) > 55 {
			detail = detail[:55] + "..."
		}
		reason := ""
		if res.Reason != "" {
			reason = "  ← " + strings.TrimPrefix(res.Reason, "[aflock] BLOCKED: ")
		}
		b.WriteString(fmt.Sprintf("%-4d  %-12s  %-8s  %s%s\n",
			res.Action.Index, res.Action.Tool, res.Decision, detail, reason))
	}

	return b.String()
}

// renderSummaryDashboard renders the summary dashboard view.
func (m replayModel) renderSummaryDashboard() string {
	r := m.report
	if r == nil {
		return ""
	}

	var b strings.Builder
	sep := dimStyle.Render("  " + strings.Repeat("─", clampLineW(m.width)))

	// Tool breakdown
	b.WriteString(section("TOOL BREAKDOWN", ""))

	type toolStats struct {
		name  string
		allow int
		deny  int
		ask   int
		total int
	}

	toolMap := make(map[string]*toolStats)
	for _, res := range r.Results {
		ts, ok := toolMap[res.Action.Tool]
		if !ok {
			ts = &toolStats{name: res.Action.Tool}
			toolMap[res.Action.Tool] = ts
		}
		ts.total++
		switch res.Decision {
		case "ALLOW":
			ts.allow++
		case "DENY":
			ts.deny++
		case "ASK":
			ts.ask++
		}
	}

	// Sort by total descending
	tools := make([]*toolStats, 0, len(toolMap))
	for _, ts := range toolMap {
		tools = append(tools, ts)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].total > tools[j].total
	})

	hdr := fmt.Sprintf("  %-14s  %-6s  %-6s  %-6s  %s", "TOOL", "ALLOW", "DENY", "ASK", "TOTAL")
	b.WriteString(dimStyle.Render(hdr) + "\n")
	lw := clampLineW(m.width)
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", lw)) + "\n")

	for _, ts := range tools {
		line := fmt.Sprintf("  %-14s  %s  %s  %s  %s",
			cyanStyle.Render(ts.name),
			greenStyle.Render(fmt.Sprintf("%-6d", ts.allow)),
			redStyle.Render(fmt.Sprintf("%-6d", ts.deny)),
			yellowStyle.Render(fmt.Sprintf("%-6d", ts.ask)),
			dimStyle.Render(fmt.Sprintf("%d", ts.total)))
		b.WriteString(line + "\n")
	}

	// Decision timeline
	b.WriteString("\n" + sep + "\n")
	b.WriteString(section("DECISION TIMELINE", ""))
	b.WriteString("  ")

	maxTimelineWidth := clampLineW(m.width)
	count := 0
	for _, res := range r.Results {
		if count >= maxTimelineWidth {
			break
		}
		switch res.Decision {
		case "ALLOW":
			b.WriteString(greenStyle.Render("\u2588"))
		case "DENY":
			b.WriteString(redStyle.Render("\u2588"))
		case "ASK":
			b.WriteString(yellowStyle.Render("\u2588"))
		default:
			b.WriteString(dimStyle.Render("\u2588"))
		}
		count++
	}
	b.WriteString("\n")
	b.WriteString("  " + dimStyle.Render(
		greenStyle.Render("\u2588")+" allow  "+
			redStyle.Render("\u2588")+" deny  "+
			yellowStyle.Render("\u2588")+" ask") + "\n")

	// Denied actions
	if r.DenyCount > 0 {
		b.WriteString("\n" + sep + "\n")
		b.WriteString(section("DENIED ACTIONS", ""))

		for _, res := range r.Results {
			if res.Decision != "DENY" {
				continue
			}
			reason := strings.TrimPrefix(res.Reason, "[aflock] BLOCKED: ")
			b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
				redStyle.Render(fmt.Sprintf("#%-3d", res.Action.Index)),
				cyanStyle.Render(res.Action.Tool),
				redStyle.Render(reason)))
		}
	}

	return b.String()
}
