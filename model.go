package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type view int

const (
	viewList        view = iota
	viewDetail
	viewAttest
	viewAttestDetail
	viewJWT
	viewVerify
)

type detailTab int

const (
	tabInspect detailTab = iota
	tabActions
	tabPolicy
	tabState
	tabCount
)

type attestTab int

const (
	attTabStructured attestTab = iota
	attTabDecoded
	attTabEncoded
	attTabCount
)

type model struct {
	sessions     []SessionInfo
	filtered     []int // indices into sessions matching filter
	state        *SessionState
	attestations []AttestationInfo
	err          error
	copied       string

	view      view
	detailTab detailTab
	attestTab attestTab
	cursor    int
	attCursor int
	scroll    int
	viewport  viewport.Model
	width     int
	height    int
	ready     bool

	// Filter
	filtering   bool
	filterInput textinput.Model
	filterText  string

	// Delete confirmation
	confirmDelete bool

	// Verify view state — set when user presses `v` on the inspect view.
	// runningVerify is true while `aflock verify` is executing async; the
	// verifyOutput viewport shows a spinner-ish placeholder during that
	// window and the result text once verifyResult comes back.
	runningVerify bool
	verifyOutput  string
}

func newModel() model {
	ti := textinput.New()
	ti.Placeholder = "filter sessions..."
	ti.CharLimit = 60
	return model{view: viewList, filterInput: ti}
}

type sessionsLoaded struct {
	sessions []SessionInfo
	err      error
}

type clearCopied struct{}

func loadSessionsCmd() tea.Msg {
	sessions, err := loadSessions()
	return sessionsLoaded{sessions, err}
}

func (m model) Init() tea.Cmd {
	return loadSessionsCmd
}

// visibleSessions returns the sessions matching the current filter.
func (m *model) visibleSessions() []SessionInfo {
	if len(m.filtered) == 0 && m.filterText == "" {
		return m.sessions
	}
	result := make([]SessionInfo, len(m.filtered))
	for i, idx := range m.filtered {
		result[i] = m.sessions[idx]
	}
	return result
}

func (m *model) applyFilter() {
	m.filtered = nil
	if m.filterText == "" {
		// Show all
		for i := range m.sessions {
			m.filtered = append(m.filtered, i)
		}
	} else {
		q := strings.ToLower(m.filterText)
		for i, s := range m.sessions {
			// Match against ID, policy name from preview, date
			searchable := strings.ToLower(s.ID + " " + s.ModTime.Format("02 Jan 15:04"))
			if s.Preview != nil {
				searchable += " " + strings.ToLower(s.Preview.PolicyName)
			}
			if strings.Contains(searchable, q) {
				m.filtered = append(m.filtered, i)
			}
		}
	}
	m.cursor = 0
	m.scroll = 0
}

func (m *model) currentSession() *SessionInfo {
	vis := m.visibleSessions()
	if m.cursor < len(vis) {
		s := vis[m.cursor]
		return &s
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionsLoaded:
		m.sessions = msg.sessions
		m.err = msg.err
		// Lazy-load previews
		for i := range m.sessions {
			m.sessions[i].Preview = loadSessionPreview(m.sessions[i].Dir)
		}
		m.applyFilter()
		return m, nil

	case clearCopied:
		m.copied = ""
		return m, nil

	case verifyResult:
		// Async result of `aflock verify --session <id>` launched by 'v'.
		m.runningVerify = false
		m.verifyOutput = msg.output
		m.updateViewport()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerH := 3
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerH)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerH
		}
		m.updateViewport()
		return m, nil

	case tea.KeyMsg:
		m.copied = ""

		// If in filter mode, handle text input
		if m.filtering {
			switch msg.String() {
			case "enter":
				// Keep filter, exit filter mode
				m.filtering = false
				m.filterInput.Blur()
				return m, nil
			case "esc":
				// Clear filter and exit
				m.filtering = false
				m.filterInput.Blur()
				m.filterText = ""
				m.filterInput.SetValue("")
				m.applyFilter()
				return m, nil
			case "up", "down":
				// Exit filter mode and navigate
				m.filtering = false
				m.filterInput.Blur()
				// Fall through to normal key handling below
			default:
				var cmd tea.Cmd
				m.filterInput, cmd = m.filterInput.Update(msg)
				m.filterText = m.filterInput.Value()
				m.applyFilter()
				return m, cmd
			}
		}

		// If confirming delete
		if m.confirmDelete {
			switch msg.String() {
			case "y", "Y":
				m.doDelete()
				m.confirmDelete = false
				return m, loadSessionsCmd
			default:
				m.confirmDelete = false
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			if m.view == viewList {
				return m, tea.Quit
			}
			m.view = viewList
			m.updateViewport()
			return m, nil

		case "esc":
			switch m.view {
			case viewList:
				if m.filterText != "" {
					m.filterText = ""
					m.filterInput.SetValue("")
					m.applyFilter()
					return m, nil
				}
			case viewDetail, viewJWT, viewVerify:
				m.view = viewList
			case viewAttest:
				m.view = viewDetail
				m.detailTab = tabInspect
			case viewAttestDetail:
				m.view = viewAttest
			}
			m.updateViewport()
			return m, nil

		case "enter":
			return m.handleEnter()

		case "up", "k":
			return m.handleUp()

		case "down", "j":
			return m.handleDown()

		case "left", "h":
			switch m.view {
			case viewDetail:
				m.detailTab = (m.detailTab + tabCount - 1) % tabCount
				m.updateViewport()
			case viewAttestDetail:
				m.attestTab = (m.attestTab + attTabCount - 1) % attTabCount
				m.updateViewport()
			}
			return m, nil

		case "right", "l":
			switch m.view {
			case viewDetail:
				m.detailTab = (m.detailTab + 1) % tabCount
				m.updateViewport()
			case viewAttestDetail:
				m.attestTab = (m.attestTab + 1) % attTabCount
				m.updateViewport()
			}
			return m, nil

		case "tab":
			switch m.view {
			case viewDetail:
				m.detailTab = (m.detailTab + 1) % tabCount
				m.updateViewport()
			case viewAttestDetail:
				m.attestTab = (m.attestTab + 1) % attTabCount
				m.updateViewport()
			}
			return m, nil

		case "shift+tab":
			switch m.view {
			case viewDetail:
				m.detailTab = (m.detailTab + tabCount - 1) % tabCount
				m.updateViewport()
			case viewAttestDetail:
				m.attestTab = (m.attestTab + attTabCount - 1) % attTabCount
				m.updateViewport()
			}
			return m, nil

		case "a":
			if m.view == viewDetail {
				m.view = viewAttest
				m.attCursor = 0
				m.updateViewport()
			}
			return m, nil

		case "t":
			if m.view == viewDetail && m.state != nil && m.state.AuthToken != "" {
				m.view = viewJWT
				m.updateViewport()
			}
			return m, nil

		case "v":
			// Run `aflock verify --session <id>` async + show result.
			// Triggered from the inspect view; the verify view shows a
			// "running…" placeholder until the verifyResult msg comes back.
			if m.view == viewDetail && m.state != nil {
				sessionDir := ""
				if s := m.currentSession(); s != nil {
					sessionDir = s.Dir
				}
				m.view = viewVerify
				m.runningVerify = true
				m.verifyOutput = "running aflock verify…"
				m.updateViewport()
				return m, runVerifyCmd(m.state.SessionID, sessionDir)
			}
			return m, nil

		case "r":
			if m.view == viewList {
				return m, loadSessionsCmd
			}
			return m, nil

		case "c":
			return m.handleCopy()

		case "p":
			return m.handleCopyPath()

		case "/":
			if m.view == viewList {
				m.filtering = true
				m.filterInput.Focus()
				return m, textinput.Blink
			}
			return m, nil

		case "d":
			if m.view == viewList {
				s := m.currentSession()
				if s != nil {
					m.confirmDelete = true
				}
			}
			return m, nil

		case "o":
			return m.handleOpen(false)

		case "O":
			return m.handleOpen(true)
		}

		// Forward to viewport for scrolling
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			switch m.view {
			case viewList:
				if m.cursor > 0 {
					m.cursor--
					if m.cursor < m.scroll {
						m.scroll = m.cursor
					}
				}
			case viewAttest:
				if m.attCursor > 0 {
					m.attCursor--
				}
				m.updateViewport()
			default:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}

		case tea.MouseButtonWheelDown:
			switch m.view {
			case viewList:
				vis := m.visibleSessions()
				if m.cursor < len(vis)-1 {
					m.cursor++
					maxVisible := m.height - 9
					if maxVisible < 1 {
						maxVisible = 10
					}
					if m.cursor >= m.scroll+maxVisible {
						m.scroll = m.cursor - maxVisible + 1
					}
				}
			case viewAttest:
				if m.attCursor < len(m.attestations)-1 {
					m.attCursor++
				}
				m.updateViewport()
			default:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

func (m *model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewList:
		s := m.currentSession()
		if s == nil {
			return m, nil
		}
		state, err := loadSessionState(s.Dir)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.state = state
		attestations, _ := loadAttestations(s.Dir)
		m.attestations = attestations
		m.view = viewDetail
		m.detailTab = tabInspect
		m.updateViewport()

	case viewAttest:
		if m.attCursor < len(m.attestations) {
			m.view = viewAttestDetail
			m.attestTab = attTabStructured
			m.updateViewport()
		}
	}
	return m, nil
}

func (m *model) handleUp() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewList:
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.scroll {
				m.scroll = m.cursor
			}
		}
	case viewAttest:
		if m.attCursor > 0 {
			m.attCursor--
			m.updateViewport()
		}
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyUp})
		return m, cmd
	}
	return m, nil
}

func (m *model) handleDown() (tea.Model, tea.Cmd) {
	switch m.view {
	case viewList:
		vis := m.visibleSessions()
		if m.cursor < len(vis)-1 {
			m.cursor++
			maxVisible := m.height - 9
			if maxVisible < 1 {
				maxVisible = 10
			}
			if m.cursor >= m.scroll+maxVisible {
				m.scroll = m.cursor - maxVisible + 1
			}
		}
	case viewAttest:
		if m.attCursor < len(m.attestations)-1 {
			m.attCursor++
			m.updateViewport()
		}
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(tea.KeyMsg{Type: tea.KeyDown})
		return m, cmd
	}
	return m, nil
}

func (m *model) copyToClipboard(content, label string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		m.copied = "copy failed"
	} else {
		m.copied = label + " copied"
	}
	return m, nil
}

func (m *model) handleCopy() (tea.Model, tea.Cmd) {
	var content, label string

	switch m.view {
	case viewAttestDetail:
		if m.attCursor < len(m.attestations) {
			a := m.attestations[m.attCursor]
			switch m.attestTab {
			case attTabStructured, attTabDecoded:
				content = a.Decoded
				label = "decoded JSON"
			case attTabEncoded:
				content = a.RawJSON
				label = "raw envelope"
			}
		}
	case viewJWT:
		if m.state != nil {
			content = m.state.AuthToken
			label = "JWT token"
		}
	case viewDetail:
		if m.state != nil {
			data, _ := json.MarshalIndent(m.state, "", "  ")
			content = string(data)
			label = "state.json"
		}
	}

	return m.copyToClipboard(content, label)
}

func (m *model) handleCopyPath() (tea.Model, tea.Cmd) {
	var path string

	switch m.view {
	case viewList:
		if s := m.currentSession(); s != nil {
			path = s.Dir
		}
	case viewDetail:
		if s := m.currentSession(); s != nil {
			path = s.Dir + "/state.json"
		}
	case viewAttest:
		if m.attCursor < len(m.attestations) {
			path = m.attestations[m.attCursor].Path
		}
	case viewAttestDetail:
		if m.attCursor < len(m.attestations) {
			path = m.attestations[m.attCursor].Path
		}
	case viewJWT:
		if s := m.currentSession(); s != nil {
			path = s.Dir + "/state.json"
		}
	}

	return m.copyToClipboard(path, "path")
}

func (m *model) handleOpen(finder bool) (tea.Model, tea.Cmd) {
	var dir string

	switch m.view {
	case viewList, viewDetail:
		if s := m.currentSession(); s != nil {
			dir = s.Dir
		}
	case viewAttest, viewAttestDetail:
		if s := m.currentSession(); s != nil {
			dir = s.Dir + "/attestations"
		}
	}

	if dir == "" {
		return m, nil
	}

	if finder {
		// Open in Finder
		exec.Command("open", dir).Start()
		m.copied = "opened in Finder"
	} else {
		// Copy cd command to clipboard
		return m.copyToClipboard("cd "+dir, "cd command")
	}
	return m, nil
}

func (m *model) doDelete() {
	s := m.currentSession()
	if s == nil {
		return
	}
	os.RemoveAll(s.Dir)
}

func (m *model) updateViewport() {
	if !m.ready {
		return
	}
	var content string
	switch m.view {
	case viewDetail:
		content = m.renderDetail()
	case viewAttest:
		content = m.renderAttestList()
	case viewAttestDetail:
		content = m.renderAttestDetail()
	case viewJWT:
		content = m.renderJWT()
	case viewVerify:
		content = m.renderVerify()
	}
	m.viewport.SetContent(content)
	m.viewport.GotoTop()
}

// renderVerify draws the output of `aflock verify --session <id>` plus
// the signing-identity banner. While the verify command is still running,
// shows a placeholder.
func (m model) renderVerify() string {
	var b strings.Builder
	b.WriteString(section("VERIFY", "6-phase pipeline"))
	if m.runningVerify {
		b.WriteString("  " + dimStyle.Render("running aflock verify…") + "\n")
		return b.String()
	}
	if m.verifyOutput == "" {
		b.WriteString("  " + dimStyle.Render("(no output)") + "\n")
		return b.String()
	}
	// The verifyOutput already has section dividers and a summary —
	// render it as-is in dim style for the body, with PASS/FAIL markers
	// recolored.
	for _, line := range strings.Split(m.verifyOutput, "\n") {
		switch {
		case strings.Contains(line, "✓"):
			b.WriteString("  " + greenStyle.Render(line) + "\n")
		case strings.Contains(line, "✗"):
			b.WriteString("  " + redStyle.Render(line) + "\n")
		case strings.Contains(line, "⚠"):
			b.WriteString("  " + yellowStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "summary:"):
			b.WriteString("  " + cyanStyle.Render(line) + "\n")
		case strings.HasPrefix(line, "──"):
			b.WriteString("  " + dimStyle.Render(line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

// ─── Styles ─────────────────────────────────────────

var (
	purple = lipgloss.Color("#7B61FF")
	bg     = lipgloss.Color("#1a1a2e")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#fff")).
			Background(purple).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#000")).
			Background(purple).
			Padding(0, 1)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ccc")).
			Padding(0, 1)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555"))

	greenStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00FF88")).
			Bold(true)

	redStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5555")).
			Bold(true)

	yellowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD700"))

	cyanStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00BFFF"))

	orangeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF8C00"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(purple).
			PaddingLeft(1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#fff")).
			Background(purple).
			Padding(0, 2)

	tabInactiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666")).
			Background(lipgloss.Color("#1a1a1a")).
			Padding(0, 2)

	tabBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#111")).
			PaddingLeft(1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Background(bg).
			Padding(0, 1)

	copiedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000")).
			Background(lipgloss.Color("#00FF88")).
			Bold(true).
			Padding(0, 1)

	previewBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bbb")).
			Background(lipgloss.Color("#111")).
			Padding(0, 1)

	confirmStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#fff")).
			Background(lipgloss.Color("#FF5555")).
			Bold(true).
			Padding(0, 1)

	// JSON syntax colors
	jsonKeyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00BFFF"))
	jsonStrStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	jsonNumStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D19A66"))
	jsonBoolStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#C678DD"))
	jsonBracketStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
)

// ─── View rendering ─────────────────────────────────

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.", m.err)
	}

	switch m.view {
	case viewList:
		return m.renderSessionList()
	default:
		header := m.renderHeader()
		footer := m.renderFooter()
		return header + m.viewport.View() + "\n" + footer
	}
}

func (m model) renderHeader() string {
	var title string
	switch m.view {
	case viewDetail:
		if m.state != nil {
			title = fmt.Sprintf(" %s ", m.state.SessionID)
		}
	case viewAttest:
		title = " Attestations "
	case viewAttestDetail:
		if m.attCursor < len(m.attestations) {
			a := m.attestations[m.attCursor]
			tool := "?"
			if a.Predicate != nil {
				tool = a.Predicate.ToolName
			}
			title = fmt.Sprintf(" Attestation: %s ", tool)
		}
	case viewJWT:
		title = " JWT Token "
	}

	left := titleStyle.Render(title)

	policyBadge := ""
	if m.view == viewDetail && m.state != nil && m.state.Policy != nil {
		policyBadge = "  " + subtitleStyle.Render(m.state.Policy.Name)
	}

	return left + policyBadge + "\n\n"
}

func (m model) renderFooter() string {
	var parts []string

	switch m.view {
	case viewDetail:
		parts = append(parts, "</>:tabs", "a:attestations", "t:jwt", "c:copy", "p:path", "o:cd", "esc:back")
	case viewAttest:
		parts = append(parts, "j/k:select", "enter:open", "p:path", "esc:back")
	case viewAttestDetail:
		parts = append(parts, "</>:tabs", "c:copy", "p:path", "o:cd", "j/k:scroll", "esc:back")
	case viewJWT:
		parts = append(parts, "c:copy", "p:path", "j/k:scroll", "esc:back")
	}

	footer := statusBarStyle.Render(" " + strings.Join(parts, "  "))

	if m.copied != "" {
		footer = copiedStyle.Render(" "+m.copied+" ") + "  " + footer
	}

	return footer
}

func (m model) renderSessionList() string {
	var b strings.Builder

	left := titleStyle.Render(" aflock sessions ")
	vis := m.visibleSessions()
	countText := fmt.Sprintf(" %d sessions", len(vis))
	if m.filterText != "" {
		countText += fmt.Sprintf(" (filtered: %q)", m.filterText)
	}
	b.WriteString(left + dimStyle.Render(countText) + "\n")

	hdr := fmt.Sprintf("  %-6s  %-16s  %-50s  %5s  %3s  %s",
		"SIZE", "MODIFIED", "SESSION", "FILES", "ATT", "JWT")
	b.WriteString(dimStyle.Render(hdr) + "\n")
	lineW := min(m.width-4, 110)
	if lineW < 1 {
		lineW = 80
	}
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", lineW)) + "\n")

	if len(vis) == 0 {
		if m.filterText != "" {
			b.WriteString("\n  No sessions match filter\n")
		} else {
			b.WriteString("\n  No sessions found in ~/.aflock/sessions/\n")
		}
		b.WriteString("\n")
		b.WriteString(m.renderListFooter())
		return b.String()
	}

	maxVisible := m.height - 9
	if maxVisible < 1 {
		maxVisible = 10
	}

	for i := m.scroll; i < len(vis) && i < m.scroll+maxVisible; i++ {
		s := vis[i]

		jwt := dimStyle.Render("--")
		if s.HasJWT {
			jwt = greenStyle.Render("OK")
		}

		attStr := fmt.Sprintf("%d", s.AttestCount)
		if s.AttestCount > 0 {
			attStr = orangeStyle.Render(attStr)
		} else {
			attStr = dimStyle.Render(attStr)
		}

		line := fmt.Sprintf("%-6s  %-16s  %-50s  %5d  %3s  %s",
			humanSize(s.Size),
			s.ModTime.Format("02 Jan 15:04"),
			truncate(s.ID, 50),
			s.FileCount, attStr, jwt)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(" > "+line) + "\n")
		} else {
			b.WriteString(normalStyle.Render("   "+line) + "\n")
		}
	}

	// Preview bar
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", lineW)) + "\n")
	b.WriteString(m.renderPreview(vis))
	b.WriteString("\n")

	// Filter input or footer
	if m.filtering {
		b.WriteString("  " + m.filterInput.View() + "\n")
	} else if m.confirmDelete {
		s := m.currentSession()
		name := ""
		if s != nil {
			name = s.ID
		}
		b.WriteString(confirmStyle.Render(fmt.Sprintf(" Delete session %s? (y/N) ", truncate(name, 40))) + "\n")
	} else {
		b.WriteString(m.renderListFooter())
	}

	if m.copied != "" {
		b.WriteString(copiedStyle.Render(" "+m.copied+" ") + "\n")
	}

	return b.String()
}

func (m model) renderPreview(vis []SessionInfo) string {
	if m.cursor >= len(vis) {
		return ""
	}
	s := vis[m.cursor]
	p := s.Preview
	if p == nil {
		return previewBarStyle.Render("  " + dimStyle.Render("(no state.json)"))
	}

	var parts []string

	if p.PolicyName != "" {
		parts = append(parts, subtitleStyle.Render(p.PolicyName))
	}
	parts = append(parts, dimStyle.Render(fmt.Sprintf("%d calls", p.ToolCalls)))

	if p.AllowCount > 0 || p.DenyCount > 0 {
		parts = append(parts,
			greenStyle.Render(fmt.Sprintf("%d allow", p.AllowCount)),
			redStyle.Render(fmt.Sprintf("%d deny", p.DenyCount)))
	}

	if len(p.Tools) > 0 {
		var tools []string
		for t, c := range p.Tools {
			tools = append(tools, fmt.Sprintf("%s:%d", t, c))
		}
		parts = append(parts, dimStyle.Render(strings.Join(tools, " ")))
	}

	parts = append(parts, dimStyle.Render(fmt.Sprintf("$%.4f", p.CostUSD)))

	return previewBarStyle.Render("  " + strings.Join(parts, dimStyle.Render("  |  ")))
}

func (m model) renderListFooter() string {
	return statusBarStyle.Render(" enter:inspect  /:filter  d:delete  o:cd  O:finder  r:refresh  esc:back  q:quit")
}

func (m model) renderDetail() string {
	if m.state == nil {
		return "No session loaded"
	}

	tabs := []string{" Inspect ", " Actions ", " Policy ", " State "}
	var tabBar strings.Builder
	for i, t := range tabs {
		if detailTab(i) == m.detailTab {
			tabBar.WriteString(tabActiveStyle.Render(t))
		} else {
			tabBar.WriteString(tabInactiveStyle.Render(t))
		}
	}
	result := tabBarStyle.Render(tabBar.String()) + "\n\n"

	switch m.detailTab {
	case tabInspect:
		return result + m.renderInspect()
	case tabActions:
		return result + m.renderActions()
	case tabPolicy:
		return result + m.renderPolicy()
	case tabState:
		return result + m.renderRawState()
	}
	return result
}

func (m model) renderInspect() string {
	s := m.state
	var b strings.Builder

	b.WriteString(section("WHO", "Agent Identity"))
	if s.Identity != nil {
		model := cyanStyle.Render(s.Identity.Model)
		if s.Identity.ModelVersion != "" {
			model += " " + dimStyle.Render("@"+s.Identity.ModelVersion)
		}
		b.WriteString(kv("Model", model))
		binary := s.Identity.BinaryName
		if s.Identity.BinaryVersion != "" {
			binary += " " + dimStyle.Render("@"+s.Identity.BinaryVersion)
		}
		b.WriteString(kv("Binary", binary))
		if s.Identity.Environment != "" {
			b.WriteString(kv("Env", s.Identity.Environment))
		}
		b.WriteString(kv("Hash", dimStyle.Render(truncate(s.Identity.IdentityHash, 32)+"...")))
	} else if s.AuthToken != "" {
		b.WriteString("  " + dimStyle.Render("identity embedded in JWT — press t to decode") + "\n")
	} else {
		b.WriteString("  " + dimStyle.Render("no identity metadata") + "\n")
	}
	b.WriteString("\n")

	b.WriteString(section("WHAT", "Actions"))
	if s.Metrics != nil {
		b.WriteString(kv("Tool Calls", cyanStyle.Render(fmt.Sprintf("%d", s.Metrics.ToolCalls))))
		b.WriteString(kv("Turns", fmt.Sprintf("%d", s.Metrics.Turns)))
		if len(s.Metrics.Tools) > 0 {
			var tools []string
			for t, c := range s.Metrics.Tools {
				tools = append(tools, cyanStyle.Render(t)+dimStyle.Render(fmt.Sprintf(":%d", c)))
			}
			b.WriteString(kv("Tools", strings.Join(tools, "  ")))
		}
	}
	allowCount, denyCount := 0, 0
	for _, a := range s.Actions {
		if a.Decision == "allow" {
			allowCount++
		} else {
			denyCount++
		}
	}
	if len(s.Actions) > 0 {
		b.WriteString(kv("Decisions",
			greenStyle.Render(fmt.Sprintf(" %d allow ", allowCount))+"  "+
				redStyle.Render(fmt.Sprintf(" %d deny ", denyCount))))
	}
	b.WriteString("\n")

	b.WriteString(section("WHEN", "Timing & Cost"))
	b.WriteString(kv("Started", s.StartedAt.Format("2006-01-02 15:04:05")))
	if cs := m.currentSession(); cs != nil {
		b.WriteString(kv("Session Dir", dimStyle.Render(cs.Dir)+" "+dimStyle.Render("(p to copy)")))
	}
	if s.Metrics != nil {
		costColor := greenStyle
		if s.Metrics.CostUSD > 1.0 {
			costColor = redStyle
		} else if s.Metrics.CostUSD > 0.1 {
			costColor = yellowStyle
		}
		b.WriteString(kv("Cost", costColor.Render(fmt.Sprintf("$%.4f", s.Metrics.CostUSD))))
		b.WriteString(kv("Tokens", fmt.Sprintf("in: %s  out: %s",
			cyanStyle.Render(fmt.Sprintf("%d", s.Metrics.TokensIn)),
			cyanStyle.Render(fmt.Sprintf("%d", s.Metrics.TokensOut)))))
	}
	b.WriteString("\n")

	b.WriteString(section("PROOF", "Cryptographic Evidence"))
	if len(m.attestations) > 0 {
		b.WriteString(kv("Attestations", greenStyle.Render(fmt.Sprintf("%d signed", len(m.attestations)))+" "+dimStyle.Render("press a")))
	} else {
		b.WriteString(kv("Attestations", dimStyle.Render("0")))
	}
	if s.AuthToken != "" {
		b.WriteString(kv("JWT", greenStyle.Render("present")+" "+dimStyle.Render("press t")))
	} else {
		b.WriteString(kv("JWT", dimStyle.Render("none")))
	}
	b.WriteString(kv("Verify", dimStyle.Render("press v to run aflock verify")))

	return b.String()
}

func (m model) renderActions() string {
	if m.state == nil || len(m.state.Actions) == 0 {
		return "  " + dimStyle.Render("No actions recorded")
	}

	var b strings.Builder
	b.WriteString(subtitleStyle.Render(fmt.Sprintf("  %d actions", len(m.state.Actions))) + "\n\n")

	hdr := fmt.Sprintf("  %-4s  %-8s  %-6s  %-12s  %s", "#", "TIME", "RESULT", "TOOL", "REASON")
	b.WriteString(dimStyle.Render(hdr) + "\n")
	actLineW := min(m.width-4, 80)
	if actLineW < 1 {
		actLineW = 60
	}
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", actLineW)) + "\n")

	for i, a := range m.state.Actions {
		icon := greenStyle.Render("ALLOW")
		if a.Decision != "allow" {
			icon = redStyle.Render("DENY ")
		}
		ts := a.Timestamp.Format("15:04:05")
		reason := ""
		if a.Reason != "" {
			reason = dimStyle.Render(truncate(a.Reason, 50))
		}
		fmt.Fprintf(&b, "  %-4s  %s  %s  %-12s  %s\n",
			dimStyle.Render(fmt.Sprintf("%d", i+1)),
			dimStyle.Render(ts), icon, cyanStyle.Render(a.ToolName), reason)
	}
	return b.String()
}

func (m model) renderPolicy() string {
	if m.state == nil || m.state.Policy == nil {
		return "  " + dimStyle.Render("No policy loaded")
	}

	p := m.state.Policy
	var b strings.Builder

	b.WriteString(kv("Name", subtitleStyle.Render(p.Name)))
	b.WriteString(kv("Version", p.Version))
	b.WriteString(kv("Path", dimStyle.Render(m.state.PolicyPath)))
	b.WriteString("\n")

	if p.Limits != nil {
		b.WriteString(section("", "Limits"))
		if p.Limits.MaxSpendUSD != nil {
			b.WriteString(kv("maxSpendUSD", yellowStyle.Render(fmt.Sprintf("$%.2f", p.Limits.MaxSpendUSD.Value))+" "+dimStyle.Render(p.Limits.MaxSpendUSD.Enforcement)))
		}
		if p.Limits.MaxTurns != nil {
			b.WriteString(kv("maxTurns", yellowStyle.Render(fmt.Sprintf("%.0f", p.Limits.MaxTurns.Value))+" "+dimStyle.Render(p.Limits.MaxTurns.Enforcement)))
		}
		b.WriteString("\n")
	}

	if p.Tools != nil {
		b.WriteString(section("", "Tools"))
		if len(p.Tools.Allow) > 0 {
			var items []string
			for _, t := range p.Tools.Allow {
				items = append(items, greenStyle.Render(t))
			}
			b.WriteString(kv("Allow", strings.Join(items, dimStyle.Render(", "))))
		}
		if len(p.Tools.Deny) > 0 {
			var items []string
			for _, t := range p.Tools.Deny {
				items = append(items, redStyle.Render(t))
			}
			b.WriteString(kv("Deny", strings.Join(items, dimStyle.Render(", "))))
		}
		b.WriteString("\n")
	}

	if p.Files != nil {
		b.WriteString(section("", "Files"))
		if len(p.Files.Allow) > 0 {
			b.WriteString(kv("Allow", greenStyle.Render(strings.Join(p.Files.Allow, ", "))))
		}
		if len(p.Files.Deny) > 0 {
			b.WriteString(kv("Deny", redStyle.Render(strings.Join(p.Files.Deny, ", "))))
		}
		if len(p.Files.ReadOnly) > 0 {
			b.WriteString(kv("ReadOnly", yellowStyle.Render(strings.Join(p.Files.ReadOnly, ", "))))
		}
	}
	return b.String()
}

func (m model) renderRawState() string {
	if m.state == nil {
		return ""
	}
	data, _ := json.MarshalIndent(m.state, "", "  ")
	return colorizeJSON(string(data))
}

// ─── Attestation list view ──────────────────────────

func (m model) renderAttestList() string {
	if len(m.attestations) == 0 {
		return "  " + dimStyle.Render("No attestations found for this session")
	}

	var b strings.Builder
	b.WriteString(subtitleStyle.Render(fmt.Sprintf("  %d attestations", len(m.attestations))) + "\n\n")

	hdr := fmt.Sprintf("  %-4s  %-12s  %-8s  %6s  %-6s  %s", "#", "TOOL", "TIME", "SIZE", "RESULT", "SUBJECT")
	b.WriteString(dimStyle.Render(hdr) + "\n")
	lw := min(m.width-4, 100)
	if lw < 1 {
		lw = 60
	}
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", lw)) + "\n")

	for i, a := range m.attestations {
		toolName := "?"
		decision := dimStyle.Render("--")
		subject := ""
		if a.Predicate != nil {
			toolName = a.Predicate.ToolName
			if a.Predicate.Decision == "allow" {
				decision = greenStyle.Render("ALLOW")
			} else {
				decision = redStyle.Render("DENY ")
			}
		}
		if a.Statement != nil && len(a.Statement.Subject) > 0 {
			subject = dimStyle.Render(truncate(a.Statement.Subject[0].Name, 40))
		}

		ts := a.ModTime.Format("15:04:05")
		num := fmt.Sprintf("%d", i+1)
		line := fmt.Sprintf("%-4s  %-12s  %s  %6s  %s  %s",
			dimStyle.Render(num), cyanStyle.Render(toolName),
			dimStyle.Render(ts), humanSize(a.Size), decision, subject)

		if i == m.attCursor {
			b.WriteString(selectedStyle.Render(" > "+line) + "\n")
		} else {
			b.WriteString(normalStyle.Render("   "+line) + "\n")
		}
	}

	return b.String()
}

// ─── Attestation detail view ────────────────────────

func (m model) renderAttestDetail() string {
	if m.attCursor >= len(m.attestations) {
		return ""
	}

	tabs := []string{" Structured ", " Decoded JSON ", " Raw Encoded "}
	var tabBar strings.Builder
	for i, t := range tabs {
		if attestTab(i) == m.attestTab {
			tabBar.WriteString(tabActiveStyle.Render(t))
		} else {
			tabBar.WriteString(tabInactiveStyle.Render(t))
		}
	}
	result := tabBarStyle.Render(tabBar.String()) + "\n\n"

	a := m.attestations[m.attCursor]

	switch m.attestTab {
	case attTabStructured:
		return result + m.renderAttestStructured(a)
	case attTabDecoded:
		return result + m.renderAttestDecoded(a)
	case attTabEncoded:
		return result + m.renderAttestEncoded(a)
	}
	return result
}

func (m model) renderAttestStructured(a AttestationInfo) string {
	var b strings.Builder
	sep := dimStyle.Render("  " + strings.Repeat("─", clampLineW(m.width)))

	b.WriteString(section("ENVELOPE", "DSSE"))
	b.WriteString(kv("File", dimStyle.Render(a.Filename)))
	b.WriteString(kv("Path", dimStyle.Render(a.Path)+" "+dimStyle.Render("(p to copy)")))
	if a.Envelope != nil {
		b.WriteString(kv("PayloadType", cyanStyle.Render(a.Envelope.PayloadType)))
		if len(a.Envelope.Signatures) > 0 {
			sig := a.Envelope.Signatures[0]
			if sig.KeyID != "" {
				b.WriteString(kv("KeyID", dimStyle.Render(sig.KeyID)))
			}
			b.WriteString(kv("Signature", dimStyle.Render(truncate(sig.Sig, 50)+"...")))
			if sig.Certificate != "" {
				b.WriteString(kv("Certificate", greenStyle.Render("X.509 present")))
			}
		}
	}

	if a.Statement != nil {
		b.WriteString("\n" + sep + "\n")
		b.WriteString(section("STATEMENT", "in-toto v1"))
		b.WriteString(kv("Type", dimStyle.Render(a.Statement.Type)))
		b.WriteString(kv("PredicateType", cyanStyle.Render(a.Statement.PredicateType)))
		for _, subj := range a.Statement.Subject {
			b.WriteString(kv("Subject", cyanStyle.Render(subj.Name)))
			for algo, digest := range subj.Digest {
				b.WriteString(kv("  "+algo, dimStyle.Render(digest)))
			}
		}
	}

	if a.Predicate != nil {
		b.WriteString("\n" + sep + "\n")
		b.WriteString(section("ACTION", "Predicate"))

		if a.Predicate.Decision == "allow" {
			b.WriteString(kv("Decision", greenStyle.Render("  ALLOW  ")))
		} else {
			b.WriteString(kv("Decision", redStyle.Render("  DENY   ")))
		}
		b.WriteString(kv("Tool", cyanStyle.Render(a.Predicate.ToolName)))
		b.WriteString(kv("Action", a.Predicate.Action))
		b.WriteString(kv("Timestamp", a.Predicate.Timestamp.Format("2006-01-02 15:04:05")))

		if a.Predicate.ToolInput != nil {
			var inputMap map[string]any
			if json.Unmarshal(a.Predicate.ToolInput, &inputMap) == nil {
				for ik, iv := range inputMap {
					b.WriteString(kv("  "+ik, jsonStrStyle.Render(fmt.Sprintf("%v", iv))))
				}
			}
		}

		if a.Predicate.AgentID != "" {
			b.WriteString("\n" + sep + "\n")
			b.WriteString(section("AGENT", "Identity"))
			b.WriteString(kv("AgentID", dimStyle.Render(a.Predicate.AgentID)))
		}
		if a.Predicate.AgentIdentity != nil {
			var ident map[string]any
			if json.Unmarshal(a.Predicate.AgentIdentity, &ident) == nil {
				for ik, iv := range ident {
					val := fmt.Sprintf("%v", iv)
					if len(val) > 60 {
						val = truncate(val, 60) + "..."
					}
					b.WriteString(kv("  "+ik, dimStyle.Render(val)))
				}
			}
		}

		if a.Predicate.Metrics != nil {
			b.WriteString("\n" + sep + "\n")
			b.WriteString(section("METRICS", "Cumulative"))
			var metrics map[string]any
			if json.Unmarshal(a.Predicate.Metrics, &metrics) == nil {
				for mk, mv := range metrics {
					b.WriteString(kv("  "+mk, cyanStyle.Render(fmt.Sprintf("%v", mv))))
				}
			}
		}
	}

	if a.Predicate == nil && a.Decoded != "" {
		b.WriteString("\n" + sep + "\n")
		b.WriteString(section("", "Raw Decoded Payload"))
		b.WriteString(colorizeJSON(a.Decoded))
	}

	return b.String()
}

func (m model) renderAttestDecoded(a AttestationInfo) string {
	if a.Decoded == "" {
		return "  " + dimStyle.Render("Could not decode payload")
	}
	return colorizeJSON(a.Decoded)
}

func (m model) renderAttestEncoded(a AttestationInfo) string {
	if a.RawJSON == "" {
		return "  " + dimStyle.Render("No raw data")
	}
	var raw json.RawMessage
	if json.Unmarshal([]byte(a.RawJSON), &raw) == nil {
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		return colorizeJSON(string(pretty))
	}
	return a.RawJSON
}

// ─── JWT view ───────────────────────────────────────

func (m model) renderJWT() string {
	if m.state == nil || m.state.AuthToken == "" {
		return "  " + dimStyle.Render("No JWT token in this session")
	}

	decoded, err := decodeJWT(m.state.AuthToken)
	if err != nil {
		return "  " + redStyle.Render(fmt.Sprintf("Failed to decode JWT: %v", err))
	}

	var b strings.Builder
	b.WriteString(kv("Token Length", cyanStyle.Render(fmt.Sprintf("%d bytes", len(m.state.AuthToken)))))
	b.WriteString(kv("Raw", dimStyle.Render(truncate(m.state.AuthToken, 80)+"...")))
	b.WriteString("\n")
	b.WriteString(decoded)
	return b.String()
}

// ─── Helpers ────────────────────────────────────────

func section(prefix, title string) string {
	if prefix != "" {
		return headerStyle.Render(fmt.Sprintf(" %s  %s", prefix, title)) + "\n"
	}
	return headerStyle.Render(" "+title) + "\n"
}

func kv(key, value string) string {
	k := yellowStyle.Render(fmt.Sprintf("%-16s", key))
	return fmt.Sprintf("  %s %s\n", k, value)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func clampLineW(width int) int {
	w := min(width-4, 90)
	if w < 1 {
		w = 60
	}
	return w
}

func colorizeJSON(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(colorizeLine(line))
		b.WriteByte('\n')
	}
	return b.String()
}

func colorizeLine(line string) string {
	trimmed := strings.TrimSpace(line)
	indent := line[:len(line)-len(trimmed)]

	if trimmed == "{" || trimmed == "}," || trimmed == "}" ||
		trimmed == "[" || trimmed == "]," || trimmed == "]" ||
		trimmed == "{}" || trimmed == "[]" {
		return indent + jsonBracketStyle.Render(trimmed)
	}

	if colonIdx := strings.Index(trimmed, "\": "); colonIdx > 0 {
		key := trimmed[:colonIdx+1]
		rest := trimmed[colonIdx+1:]

		coloredKey := jsonKeyStyle.Render(key)
		valPart := strings.TrimPrefix(rest, ": ")
		valPart = strings.TrimSuffix(valPart, ",")
		hadComma := strings.HasSuffix(rest, ",")

		colored := colorizeValue(valPart)
		comma := ""
		if hadComma {
			comma = dimStyle.Render(",")
		}
		return indent + coloredKey + dimStyle.Render(": ") + colored + comma
	}

	if strings.HasPrefix(trimmed, "\"") {
		val := strings.TrimSuffix(trimmed, ",")
		hadComma := strings.HasSuffix(trimmed, ",")
		comma := ""
		if hadComma {
			comma = dimStyle.Render(",")
		}
		return indent + jsonStrStyle.Render(val) + comma
	}

	return indent + trimmed
}

func colorizeValue(val string) string {
	if val == "true" || val == "false" {
		return jsonBoolStyle.Render(val)
	}
	if val == "null" {
		return dimStyle.Render(val)
	}
	if strings.HasPrefix(val, "\"") {
		return jsonStrStyle.Render(val)
	}
	if val == "{" || val == "[" || val == "{}" || val == "[]" {
		return jsonBracketStyle.Render(val)
	}
	if len(val) > 0 && (val[0] >= '0' && val[0] <= '9' || val[0] == '-') {
		return jsonNumStyle.Render(val)
	}
	return val
}
