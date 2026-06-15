// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package ui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tarikguney/agent-watch/internal/notify"
	"github.com/tarikguney/agent-watch/internal/session"
	"github.com/tarikguney/agent-watch/internal/tmux"
)

var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D4A0FF")) // Soft purple/lavender
	colHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6CB6FF")) // Soft blue
	separatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	projectStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8EC07C")) // Soft green
	promptStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7DC4A3")) // Soft mint/teal
	responseStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A0A0D0")) // Soft lavender
	tmuxStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9EC8E0")) // Soft cyan
	actionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	durationStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	pidStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cursorStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D4A0FF")) // Bright arrow indicator
	markStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFD700")) // Gold selection marker
	helpKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6CB6FF")) // Soft blue for keys
	helpTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))                // Gray for descriptions
	providerStyles = map[string]lipgloss.Style{
		"CLAUDE":  lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8800")).Bold(true),
		"COPILOT": lipgloss.NewStyle().Foreground(lipgloss.Color("#6CB6FF")).Bold(true),
		"UNKNOWN": lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}

	statusStyles = map[session.Status]lipgloss.Style{
		session.StatusThinking:     lipgloss.NewStyle().Background(lipgloss.Color("#D4A017")).Foreground(lipgloss.Color("0")).Bold(true), // Amber bg
		session.StatusToolUse:      lipgloss.NewStyle().Background(lipgloss.Color("12")).Foreground(lipgloss.Color("0")).Bold(true),      // Blue bg
		session.StatusStreaming:    lipgloss.NewStyle().Background(lipgloss.Color("14")).Foreground(lipgloss.Color("0")).Bold(true),      // Cyan bg
		session.StatusResponding:   lipgloss.NewStyle().Background(lipgloss.Color("13")).Foreground(lipgloss.Color("0")).Bold(true),      // Magenta bg
		session.StatusCompletedAgo: lipgloss.NewStyle().Background(lipgloss.Color("10")).Foreground(lipgloss.Color("0")).Bold(true),      // Green bg
		session.StatusIdle:         lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("0")),                // Gray bg
		session.StatusDone:         lipgloss.NewStyle().Background(lipgloss.Color("10")).Foreground(lipgloss.Color("0")).Bold(true),      // Green bg
		session.StatusError:        lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("0")).Bold(true),       // Red bg
		session.StatusInterrupted:  lipgloss.NewStyle().Background(lipgloss.Color("11")).Foreground(lipgloss.Color("0")).Bold(true),      // Yellow bg
		session.StatusWaiting:      lipgloss.NewStyle().Background(lipgloss.Color("6")).Foreground(lipgloss.Color("0")).Bold(true),       // Teal bg
	}
)

// cols holds computed column widths for a render pass.
type cols struct {
	pid      int
	tmux     int
	provider int
	project  int
	status   int
	action   int
	dur      int
}

// Column caps so one very long value can't starve the action column.
// Content longer than the cap is truncated with "…" at render time.
const (
	tmuxColCap           = 30
	projectColCap        = 30
	statusColCap         = 32
	notificationCooldown = 4 * time.Second
)

// computeCols calculates column widths that fit the terminal on one line.
// PID, STATUS, DURATION are small and content-sized; TMUX and PROJECT are
// content-sized up to a cap; ACTION absorbs whatever space is left. If the
// terminal is too narrow to give ACTION its minimum, TMUX shrinks first,
// then PROJECT. Cell contents are truncated to these widths at render time
// so rows never wrap.
func computeCols(sessions []session.State, now time.Time, termW int) cols {
	if termW <= 0 {
		termW = 120
	}

	hasTmux := false
	for _, s := range sessions {
		if s.TmuxSession != "" {
			hasTmux = true
			break
		}
	}

	c := cols{
		pid:      len("PID") + 2,
		provider: len("PROVIDER") + 2,
		status:   len("STATUS") + 2,
		dur:      len("DURATION") + 2,
	}
	idealProvider := len("PROVIDER") + 2
	idealProject := len("PROJECT") + 2
	idealTmux := 0
	if hasTmux {
		idealTmux = len("TMUX SESSION/WINDOW") + 2
	}

	for _, s := range sessions {
		pidStr := ""
		if s.PID > 0 {
			pidStr = fmt.Sprintf("%d", s.PID)
		}
		if w := len(pidStr) + 2; w > c.pid {
			c.pid = w
		}
		dur := ""
		if !s.StartTime.IsZero() {
			dur = session.FormatDuration(now.Sub(s.StartTime))
		}
		if w := len(dur) + 2; w > c.dur {
			c.dur = w
		}
		if w := len(providerLabel(s)) + 2; w > idealProvider {
			idealProvider = w
		}
		if w := len(statusLabel(s, now)) + 2; w > c.status {
			c.status = w
		}
		if hasTmux {
			if w := len(s.TmuxSession) + 2; w > idealTmux {
				idealTmux = w
			}
		}
		if w := len(s.ProjectName) + 2; w > idealProject {
			idealProject = w
		}
	}

	if idealTmux > tmuxColCap {
		idealTmux = tmuxColCap
	}
	if idealProject > projectColCap {
		idealProject = projectColCap
	}
	if c.status > statusColCap {
		c.status = statusColCap
	}

	numSep := 5
	if hasTmux {
		numSep = 6
	}
	separators := numSep * 3

	avail := termW - c.pid - c.provider - c.status - c.dur - separators
	minAction := len("CURRENT ACTION") + 2
	minProvider := len("PROV") + 2
	minTmux := 0
	if hasTmux {
		minTmux = len("TMUX") + 2
	}
	minProject := len("PROJ") + 2

	c.tmux = idealTmux
	c.provider = idealProvider
	c.project = idealProject
	c.action = avail - c.tmux - c.provider - c.project

	// If action is starved, steal space from tmux first, then project/provider.
	for c.action < minAction {
		shrunk := false
		if c.tmux > minTmux {
			c.tmux--
			c.action++
			shrunk = true
		}
		if c.action < minAction && c.project > minProject {
			c.project--
			c.action++
			shrunk = true
		}
		if c.action < minAction && c.provider > minProvider {
			c.provider--
			c.action++
			shrunk = true
		}
		if !shrunk {
			break // terminal too narrow; accept slight overflow
		}
	}

	return c
}

// truncate cuts s to fit in maxWidth rune cells, appending "…" when cut.
// Newlines are flattened to spaces so a cell never spans multiple rows.
// Width is approximated by rune count (accurate for ASCII; acceptable for
// CJK/emoji which mostly don't appear in tmux/project/action strings).
func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if strings.ContainsAny(s, "\r\n") {
		s = strings.ReplaceAll(s, "\r\n", " ")
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.ReplaceAll(s, "\r", " ")
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

type tickMsg time.Time

// Model is the Bubbletea model for the agent-watch dashboard.
type Model struct {
	scanner               *session.Scanner
	compact               bool
	refresh               time.Duration
	providerFilter        string
	sessions              []session.State
	cursorIdx             int
	scrollOffset          int
	expanded              map[int]bool
	termW                 int
	termH                 int
	statusMsg             string
	statusExp             time.Time
	notifier              notify.Notifier
	notificationsEnabled  bool
	mutedNotifications    map[string]bool
	notificationStates    map[string]session.Status
	notificationLastSent  map[string]time.Time
	notificationStartedAt time.Time
	killConfirmPID        int
	killConfirmLabel      string
	killing               map[int]bool
	marked                map[int]bool
	composing             bool
	promptInput           textarea.Model
	selfSendTarget        string
}

// NewModel creates a new dashboard Model, pre-populated with the scanner's
// current sessions so the first render is not blank.
func NewModel(
	scanner *session.Scanner,
	compact bool,
	refresh time.Duration,
	notifier notify.Notifier,
	notificationsEnabled bool,
) Model {
	sessions := scanner.RunningSessions()
	sortSessions(sessions)
	m := Model{
		scanner:               scanner,
		compact:               compact,
		refresh:               refresh,
		providerFilter:        "all",
		sessions:              sessions,
		expanded:              make(map[int]bool),
		termW:                 120,
		termH:                 40,
		notifier:              notifier,
		notificationsEnabled:  notificationsEnabled,
		mutedNotifications:    make(map[string]bool),
		notificationStates:    snapshotNotificationStates(notificationSessionCandidates(scanner.Sessions())),
		notificationLastSent:  make(map[string]time.Time),
		notificationStartedAt: time.Now(),
		killing:               make(map[int]bool),
		marked:                make(map[int]bool),
		promptInput:           newPromptInput(),
	}
	m.selfSendTarget = tmux.SelfTarget()
	return m
}

// newPromptInput builds the single-line text input used to compose a broadcast
// newPromptInput builds the multiline text area used to compose a broadcast
// prompt. It is not focused until the user enters compose mode. Enter inserts a
// newline; submission is handled by the compose-mode key router (Ctrl+D).
func newPromptInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Type a prompt to broadcast…  (Enter = newline)"
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(6)
	return ta
}

func (m Model) Init() tea.Cmd {
	return tea.Every(m.refresh, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW = msg.Width
		m.termH = msg.Height
		m.scrollToCursor()

	case tickMsg:
		m.scanner.LoadAll()
		allSessions := m.scanner.Sessions()
		m.processNotifications(notificationSessionCandidates(allSessions))
		visibleSessions := m.scanner.RunningSessions()
		m.sessions = filterSessions(visibleSessions, m.providerFilter)
		sortSessions(m.sessions)
		if len(m.killing) > 0 {
			alive := make(map[int]bool, len(m.sessions))
			for _, s := range m.sessions {
				alive[s.PID] = true
			}
			for pid := range m.killing {
				if !alive[pid] {
					delete(m.killing, pid)
				}
			}
		}
		count := len(m.sessions)
		if count == 0 {
			m.cursorIdx = 0
		} else if m.cursorIdx >= count {
			m.cursorIdx = count - 1
		}
		m.scrollToCursor()
		return m, tea.Every(m.refresh, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tea.KeyMsg:
		if m.killConfirmPID != 0 {
			switch msg.String() {
			case "y", "Y":
				pid := m.killConfirmPID
				label := m.killConfirmLabel
				m.killConfirmPID = 0
				m.killConfirmLabel = ""
				if err := killProcess(pid); err != nil {
					m.setStatusMessage(fmt.Sprintf("Failed to kill PID %d (%s): %v", pid, label, err), 5*time.Second)
				} else {
					if m.killing == nil {
						m.killing = make(map[int]bool)
					}
					m.killing[pid] = true
					m.setStatusMessage(fmt.Sprintf("Killing PID %d (%s)…", pid, label), 3*time.Second)
				}
			default:
				m.killConfirmPID = 0
				m.killConfirmLabel = ""
				m.setStatusMessage("Kill cancelled", 2*time.Second)
			}
			return m, nil
		}
		if m.composing {
			return m.updateComposing(msg)
		}
		switch msg.String() {
		case "up", "k":
			if m.cursorIdx > 0 {
				m.cursorIdx--
			}
		case "down", "j":
			if m.cursorIdx < len(m.sessions)-1 {
				m.cursorIdx++
			}
		case "enter":
			if m.cursorIdx < len(m.sessions) {
				pid := m.sessions[m.cursorIdx].PID
				m.expanded[pid] = !m.expanded[pid]
			}
		case " ":
			if m.cursorIdx < len(m.sessions) {
				s := m.sessions[m.cursorIdx]
				if m.isSelfPane(s) {
					m.setStatusMessage("Can't select agent-watch's own pane", 3*time.Second)
				} else {
					m.toggleMark(s.PID)
				}
			}
		case "v", "V":
			m.toggleMarkAll()
		case "esc":
			if len(m.marked) > 0 {
				m.marked = make(map[int]bool)
				m.setStatusMessage("Cleared selection", 2*time.Second)
			}
		case "s", "S":
			return m.startComposing()
		case "e":
			for _, s := range m.sessions {
				m.expanded[s.PID] = true
			}
		case "c":
			m.expanded = make(map[int]bool)
		case "a", "A":
			m.providerFilter = "all"
		case "l", "L":
			m.providerFilter = "claude"
		case "m", "M":
			m.toggleSelectedMute()
		case "n", "N":
			m.toggleNotifications()
		case "p", "P":
			m.providerFilter = "copilot"
		case "g", "G":
			if m.cursorIdx < len(m.sessions) {
				s := m.sessions[m.cursorIdx]
				if s.TmuxSession == "" {
					m.statusMsg = "Session not in tmux"
					m.statusExp = time.Now().Add(3 * time.Second)
				} else if err := tmux.SwitchToPane(s.TmuxSession, s.TmuxPaneID); err == nil {
					m.statusMsg = fmt.Sprintf("Switched to %s", s.TmuxSession)
					m.statusExp = time.Now().Add(3 * time.Second)
				} else {
					// Programmatic switch failed — show manual navigation hint
					parts := strings.SplitN(s.TmuxSession, "/", 2)
					hint := "Ctrl+B, s"
					if len(parts) == 2 {
						hint = fmt.Sprintf("Ctrl+B, s → select \"%s\" → window \"%s\"", parts[0], parts[1])
					}
					m.statusMsg = fmt.Sprintf("Go to: %s  |  %s", s.TmuxSession, hint)
					m.statusExp = time.Now().Add(5 * time.Second)
				}
			}
		case "x", "X":
			if m.cursorIdx < len(m.sessions) {
				s := m.sessions[m.cursorIdx]
				if s.PID <= 0 {
					m.setStatusMessage("Selected row has no PID to kill", 3*time.Second)
				} else if m.killing[s.PID] {
					m.setStatusMessage(fmt.Sprintf("PID %d already being killed", s.PID), 2*time.Second)
				} else {
					m.killConfirmPID = s.PID
					m.killConfirmLabel = displayProjectName(s)
				}
			}
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		}
		m.scrollToCursor()
	}
	return m, nil
}

// rowSpan is the half-open body line range [start, end) occupied by a session
// entry (its row plus any expanded prompt/response lines), excluding the
// trailing separator. Used to keep the selected row inside the scroll window.
type rowSpan struct {
	start, end int
}

// layout renders the dashboard into three stacked sections: a fixed top
// (title + column header), a scrollable body (one entry per session), and a
// fixed footer (help/status bar). spans[i] is the body line range for session i.
func (m Model) layout(now time.Time) (top, body, footer []string, spans []rowSpan) {
	c := computeCols(m.sessions, now, m.termW)

	// Title line
	title := titleStyle.Render("AGENT WATCH")
	subtitle := durationStyle.Render("— monitoring agent sessions")
	timestamp := durationStyle.Render(now.Format("01/02 15:04:05"))
	filterTag := durationStyle.Render(fmt.Sprintf("[filter: %s]", strings.ToUpper(m.providerFilter)))
	titleParts := []string{title, subtitle, filterTag}
	if m.notifier != nil {
		titleParts = append(titleParts, durationStyle.Render(m.notificationTag()))
		if mutedTag := m.selectedMutedTag(); mutedTag != "" {
			titleParts = append(titleParts, durationStyle.Render(mutedTag))
		}
	}
	if n := len(m.marked); n > 0 {
		titleParts = append(titleParts, markStyle.Render(fmt.Sprintf("[selected: %d]", n)))
	}
	titleParts = append(titleParts, timestamp)

	widths := []int{c.pid, c.provider, c.project, c.status, c.action, c.dur}
	headers := []string{
		colHeaderStyle.Width(c.pid).Render(truncate("PID", c.pid)),
		colHeaderStyle.Width(c.provider).Render(truncate("PROVIDER", c.provider)),
		colHeaderStyle.Width(c.project).Render(truncate("PROJECT", c.project)),
		colHeaderStyle.Width(c.status).Render(truncate("STATUS", c.status)),
		colHeaderStyle.Width(c.action).Render(truncate("CURRENT ACTION", c.action)),
		colHeaderStyle.Width(c.dur).Render(truncate("DURATION", c.dur)),
	}
	if c.tmux > 0 {
		widths = append(widths[:1], append([]int{c.tmux}, widths[1:]...)...)
		headers = append(headers[:1], append([]string{
			colHeaderStyle.Width(c.tmux).Render(truncate("TMUX SESSION/WINDOW", c.tmux)),
		}, headers[1:]...)...)
	}
	tw := totalWidth(widths)

	top = []string{
		strings.Join(titleParts, " "),
		hline(tw),
		joinCols(headers),
		hline(tw),
	}

	for i, s := range m.sessions {
		isCursor := i == m.cursorIdx
		isExpanded := m.expanded[s.PID]

		start := len(body)
		body = append(body, renderRow(s, now, c, isCursor, m.marked[s.PID], m.killing[s.PID]))

		if !m.compact && isExpanded {
			prompt := s.LastPrompt
			if prompt == "" {
				prompt = s.OriginalTask
			}
			if prompt != "" {
				prefix := "  » prompt: "
				maxLen := max(1, m.termW-len(prefix))
				body = append(body,
					durationStyle.Render(prefix)+promptStyle.Render(truncate(prompt, maxLen)))
			}
			if s.LastResponse != "" {
				prefix := "  » response: "
				maxLen := max(1, m.termW-len(prefix))
				body = append(body,
					durationStyle.Render(prefix)+responseStyle.Render(truncate(s.LastResponse, maxLen)))
			}
		}

		spans = append(spans, rowSpan{start: start, end: len(body)})

		if i < len(m.sessions)-1 {
			body = append(body, hline(tw))
		}
	}

	if len(m.sessions) == 0 {
		body = append(body, durationStyle.Render("  No active sessions found. Watching for new sessions..."))
	}

	// Help bar or status message (mutually exclusive to keep line count stable)
	if !m.compact {
		footer = append(footer, "", hline(tw))
		if m.composing {
			footer = append(footer,
				helpKeyStyle.Render("  Ctrl+D")+helpTextStyle.Render(" Send  ")+
					helpKeyStyle.Render("Enter")+helpTextStyle.Render(" New line  ")+
					helpKeyStyle.Render("Esc")+helpTextStyle.Render(" Cancel"))
		} else if m.killConfirmPID != 0 {
			warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true) // red
			footer = append(footer, warnStyle.Render(fmt.Sprintf("  Kill PID %d (%s)? Press y to confirm, any other key to cancel.", m.killConfirmPID, m.killConfirmLabel)))
		} else if m.statusMsg != "" && time.Now().Before(m.statusExp) {
			warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
			footer = append(footer, warnStyle.Render("  "+m.statusMsg))
		} else {
			footer = append(footer,
				helpKeyStyle.Render("↑↓")+helpTextStyle.Render(" Navigate  ")+
					helpKeyStyle.Render("Enter")+helpTextStyle.Render(" Toggle  ")+
					helpKeyStyle.Render("Space")+helpTextStyle.Render(" Select  ")+
					helpKeyStyle.Render("v")+helpTextStyle.Render(" Select All  ")+
					helpKeyStyle.Render("s")+helpTextStyle.Render(" Broadcast  ")+
					helpKeyStyle.Render("g")+helpTextStyle.Render(" Go to Window  ")+
					m.notificationHelp()+
					helpKeyStyle.Render("a/l/p")+helpTextStyle.Render(" Filter  ")+
					helpKeyStyle.Render("e")+helpTextStyle.Render(" Expand All  ")+
					helpKeyStyle.Render("c")+helpTextStyle.Render(" Collapse All  ")+
					helpKeyStyle.Render("x")+helpTextStyle.Render(" Kill  ")+
					helpKeyStyle.Render("q")+helpTextStyle.Render(" Quit"))
		}
	}

	return top, body, footer, spans
}

// visibleRows is the number of body lines that fit between the fixed top and
// footer sections for the current terminal height.
func (m Model) visibleRows(topLines, footerLines int) int {
	return max(1, m.termH-topLines-footerLines)
}

func (m Model) View() string {
	top, body, footer, spans := m.layout(time.Now())
	avail := m.visibleRows(len(top), len(footer))

	offset := max(0, min(m.scrollOffset, len(body)-avail))
	end := min(offset+avail, len(body))

	// When the body overflows the window, annotate the title with the visible
	// row range and arrows pointing to the off-screen content.
	if len(body) > avail && len(top) > 0 {
		top[0] += "  " + scrollIndicator(spans, offset, end, len(body))
	}

	lines := make([]string, 0, len(top)+(end-offset)+len(footer))
	lines = append(lines, top...)
	lines = append(lines, body[offset:end]...)
	lines = append(lines, footer...)
	base := strings.Join(lines, "\n")

	if m.composing {
		return m.overlayComposeDialog(base)
	}
	return base
}

var (
	dialogBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#D4A0FF")).
				Padding(0, 1)
	dialogTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D4A0FF"))
	dialogHelpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// overlayComposeDialog renders the multiline broadcast prompt as a centered
// modal box placed over the dashboard background.
func (m Model) overlayComposeDialog(background string) string {
	title := dialogTitleStyle.Render(
		fmt.Sprintf("Broadcast prompt → %d target(s)", len(m.broadcastTargets())))
	help := dialogHelpStyle.Render("Ctrl+D send   ·   Enter newline   ·   Esc cancel")

	inner := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		m.promptInput.View(),
		"",
		help,
	)
	dialog := dialogBorderStyle.Render(inner)

	return placeOverlay(background, dialog, m.termW, m.termH)
}

// placeOverlay centers fg over a bg of the given dimensions. lipgloss.Place
// composites the dialog onto a blank canvas sized to the terminal so the modal
// appears centered; the underlying dashboard is replaced for the frame to keep
// rendering simple and flicker-free.
func placeOverlay(_ string, fg string, w, h int) string {
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, fg)
}

// scrollIndicator builds a compact "▲ first-last/total ▼" badge for the title
// bar. Arrows are lit only in directions where content is hidden, and the range
// reports which session rows are (partially) on screen.
func scrollIndicator(spans []rowSpan, offset, end, total int) string {
	up := " "
	if offset > 0 {
		up = "▲"
	}
	down := " "
	if end < total {
		down = "▼"
	}

	label := " more "
	if len(spans) > 0 {
		first, last := len(spans)-1, 0
		for i, s := range spans {
			if s.end > offset {
				first = i
				break
			}
		}
		for i, s := range spans {
			if s.start < end {
				last = i
			}
		}
		label = fmt.Sprintf(" %d-%d/%d ", first+1, last+1, len(spans))
	}

	return helpKeyStyle.Render(up) + durationStyle.Render(label) + helpKeyStyle.Render(down)
}

// scrollToCursor adjusts scrollOffset so the selected row stays inside the
// visible window. The selection drags the scroll position with it; once the
// cursor parks at an end, the offset is clamped to reveal the remaining
// non-selectable content (down to the last body line, up to the first).
func (m *Model) scrollToCursor() {
	top, body, footer, spans := m.layout(time.Now())
	avail := m.visibleRows(len(top), len(footer))

	if m.cursorIdx >= 0 && m.cursorIdx < len(spans) {
		s := spans[m.cursorIdx]
		if s.start < m.scrollOffset {
			m.scrollOffset = s.start
		} else if s.end > m.scrollOffset+avail {
			m.scrollOffset = s.end - avail
		}
	}

	if maxOff := len(body) - avail; m.scrollOffset > maxOff {
		m.scrollOffset = maxOff
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m *Model) processNotifications(allSessions []session.State) {
	if m.notificationLastSent == nil {
		m.notificationLastSent = make(map[string]time.Time)
	}

	previous := m.notificationStates
	current := snapshotNotificationStates(allSessions)

	for _, s := range allSessions {
		// Only evaluate sessions with a live process. Notifications should track
		// what the dashboard is actively monitoring, not stale transcript entries.
		if s.PID <= 0 {
			continue
		}
		key := sessionNotificationKey(s)
		if key == "" {
			continue
		}

		prevStatus, hadPrevious := previous[key]
		shouldNotify := false
		if hadPrevious {
			shouldNotify = shouldNotifyStatusTransition(prevStatus, s.Status)
		} else if s.LastUpdate.After(m.notificationStartedAt) || s.FileModTime.After(m.notificationStartedAt) {
			shouldNotify = isNotifiableStatus(s.Status)
		}

		if !shouldNotify || !m.notificationsEnabled || !m.notifierAvailable() || m.mutedNotifications[key] {
			continue
		}
		if lastSent, ok := m.notificationLastSent[key]; ok && time.Since(lastSent) < notificationCooldown {
			continue
		}

		// Fire async: go-toast spawns powershell.exe and blocks for 2-3s.
		// Inside Bubble Tea's Update loop with the alt-screen TTY active,
		// a synchronous call freezes the UI and can prevent the toast banner
		// from rendering (only the sound plays). A goroutine isolates it.
		notification := notificationForSession(s)
		project := displayProjectName(s)
		notifier := m.notifier
		m.notificationLastSent[key] = time.Now()
		go func() {
			if err := notifier.Notify(notification); err != nil {
				log.Printf("windows notification failed for %s: %v", project, err)
			}
		}()
	}

	m.notificationStates = current
}

func (m *Model) toggleNotifications() {
	if !m.notifierAvailable() {
		m.setStatusMessage("Windows notifications are unavailable in this environment", 4*time.Second)
		return
	}

	m.notificationsEnabled = !m.notificationsEnabled
	if m.notificationsEnabled {
		m.setStatusMessage("Windows notifications enabled", 3*time.Second)
		return
	}
	m.setStatusMessage("Windows notifications disabled", 3*time.Second)
}

func (m *Model) toggleSelectedMute() {
	if !m.notifierAvailable() {
		m.setStatusMessage("Windows notifications are unavailable in this environment", 4*time.Second)
		return
	}
	if len(m.sessions) == 0 || m.cursorIdx >= len(m.sessions) {
		return
	}

	selected := m.sessions[m.cursorIdx]
	key := sessionNotificationKey(selected)
	if key == "" {
		m.setStatusMessage("Selected row cannot be muted", 3*time.Second)
		return
	}

	project := displayProjectName(selected)
	if m.mutedNotifications[key] {
		delete(m.mutedNotifications, key)
		m.setStatusMessage(fmt.Sprintf("Windows notifications unmuted for %s", project), 3*time.Second)
		return
	}

	m.mutedNotifications[key] = true
	m.setStatusMessage(fmt.Sprintf("Windows notifications muted for %s", project), 3*time.Second)
}

func (m *Model) setStatusMessage(msg string, duration time.Duration) {
	m.statusMsg = msg
	m.statusExp = time.Now().Add(duration)
}

// toggleMark flips the broadcast selection for a session PID. PIDs <= 0 can't
// be marked because they have no addressable process/pane.
func (m *Model) toggleMark(pid int) {
	if pid <= 0 {
		m.setStatusMessage("Selected row has no PID to select", 2*time.Second)
		return
	}
	if m.marked == nil {
		m.marked = make(map[int]bool)
	}
	if m.marked[pid] {
		delete(m.marked, pid)
	} else {
		m.marked[pid] = true
	}
}

// toggleMarkAll marks every visible row, or clears all marks if every visible
// row is already marked.
func (m *Model) toggleMarkAll() {
	if m.marked == nil {
		m.marked = make(map[int]bool)
	}
	allMarked := true
	eligible := 0
	for _, s := range m.sessions {
		if s.PID <= 0 || m.isSelfPane(s) {
			continue
		}
		eligible++
		if !m.marked[s.PID] {
			allMarked = false
		}
	}
	if eligible == 0 {
		return
	}
	if allMarked {
		m.marked = make(map[int]bool)
		m.setStatusMessage("Cleared selection", 2*time.Second)
		return
	}
	for _, s := range m.sessions {
		if s.PID > 0 && !m.isSelfPane(s) {
			m.marked[s.PID] = true
		}
	}
	m.setStatusMessage(fmt.Sprintf("Selected %d sessions", eligible), 2*time.Second)
}

// startComposing enters prompt-compose mode. It refuses to start when there are
// no targets (no marked rows and no row under the cursor).
func (m Model) startComposing() (tea.Model, tea.Cmd) {
	if len(m.broadcastTargets()) == 0 {
		m.setStatusMessage("No session selected to prompt", 3*time.Second)
		return m, nil
	}
	m.composing = true
	m.promptInput.Reset()
	// Size the editor to a comfortable share of the terminal.
	w := m.termW - 12
	if w > 100 {
		w = 100
	}
	if w < 20 {
		w = 20
	}
	m.promptInput.SetWidth(w)
	h := m.termH / 3
	if h < 4 {
		h = 4
	}
	if h > 12 {
		h = 12
	}
	m.promptInput.SetHeight(h)
	cmd := m.promptInput.Focus()
	return m, cmd
}

// updateComposing routes key input to the prompt field while composing. Ctrl+D
// broadcasts the prompt to the targets; Esc cancels without sending. Enter is
// passed through to the text area as a newline (multiline input). Ctrl+D is used
// for submit because Shift+Enter is not reliably distinguishable from Enter in a
// terminal without an enhanced keyboard protocol.
func (m Model) updateComposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.composing = false
		m.promptInput.Blur()
		m.setStatusMessage("Broadcast cancelled", 2*time.Second)
		return m, nil
	case "ctrl+d":
		prompt := strings.TrimSpace(m.promptInput.Value())
		m.composing = false
		m.promptInput.Blur()
		if prompt == "" {
			m.setStatusMessage("Empty prompt, nothing sent", 2*time.Second)
			return m, nil
		}
		m.broadcast(prompt)
		return m, nil
	}
	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

// broadcastTargets returns the sessions a prompt would be sent to: the marked
// set, or the cursor row when nothing is marked.
func (m Model) broadcastTargets() []session.State {
	var targets []session.State
	if len(m.marked) > 0 {
		for _, s := range m.sessions {
			if m.marked[s.PID] {
				targets = append(targets, s)
			}
		}
		return targets
	}
	if m.cursorIdx >= 0 && m.cursorIdx < len(m.sessions) {
		targets = append(targets, m.sessions[m.cursorIdx])
	}
	return targets
}

// isSelfPane reports whether the session lives in the same pane agent-watch is
// running in. Compared by the fully-qualified send target ("session:win.pane"),
// which is unambiguous across psmux servers (unlike bare pane IDs).
func (m Model) isSelfPane(s session.State) bool {
	if m.selfSendTarget == "" || s.TmuxSendTarget == "" {
		return false
	}
	return s.TmuxSendTarget == m.selfSendTarget
}

// broadcast sends prompt to every target that lives inside a multiplexer pane.
// Targets without a resolved send target are skipped, as is agent-watch's own
// pane (so the dashboard never types into itself). Results are summarized in the
// status bar. Marks are cleared on a successful send.
func (m *Model) broadcast(prompt string) {
	targets := m.broadcastTargets()
	total := len(targets)
	sent, skipped, selfSkipped, failed := 0, 0, 0, 0
	for _, s := range targets {
		if m.isSelfPane(s) {
			selfSkipped++
			continue
		}
		if s.TmuxSendTarget == "" {
			skipped++
			continue
		}
		if err := tmux.SendPrompt(s.TmuxSendTarget, prompt); err != nil {
			failed++
			log.Printf("broadcast to %s (%s) failed: %v", s.TmuxSendTarget, displayProjectName(s), err)
			continue
		}
		sent++
	}

	parts := []string{fmt.Sprintf("Sent to %d/%d agents", sent, total)}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped: not in a multiplexer", skipped))
	}
	if selfSkipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped: agent-watch's own pane", selfSkipped))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed (see log)", failed))
	}
	m.setStatusMessage(strings.Join(parts, "  |  "), 5*time.Second)
	if sent > 0 {
		m.marked = make(map[int]bool)
	}
}

func (m Model) notificationHelp() string {
	if m.notifier == nil {
		return ""
	}
	return helpKeyStyle.Render("n") + helpTextStyle.Render(" Win Notify  ") +
		helpKeyStyle.Render("m") + helpTextStyle.Render(" Mute Row  ")
}

func (m Model) notificationTag() string {
	if !m.notifierAvailable() {
		return "[win notify: unavailable]"
	}
	if m.notificationsEnabled {
		return "[win notify: on]"
	}
	return "[win notify: off]"
}

func (m Model) selectedMutedTag() string {
	if len(m.sessions) == 0 || m.cursorIdx >= len(m.sessions) {
		return ""
	}
	key := sessionNotificationKey(m.sessions[m.cursorIdx])
	if !m.mutedNotifications[key] {
		return ""
	}
	return fmt.Sprintf("[muted: %s]", displayProjectName(m.sessions[m.cursorIdx]))
}

func (m Model) notifierAvailable() bool {
	return m.notifier != nil && m.notifier.Supported()
}

func snapshotNotificationStates(sessions []session.State) map[string]session.Status {
	snapshot := make(map[string]session.Status, len(sessions))
	for _, s := range sessions {
		key := sessionNotificationKey(s)
		if key == "" {
			continue
		}
		snapshot[key] = s.Status
	}
	return snapshot
}

// notificationSessionCandidates deduplicates sessions by notification key and
// keeps the most relevant state per key.
func notificationSessionCandidates(sessions []session.State) []session.State {
	best := make(map[string]session.State)
	for _, s := range sessions {
		key := sessionNotificationKey(s)
		if key == "" {
			continue
		}
		current, ok := best[key]
		if !ok || moreRelevantNotificationState(s, current) {
			best[key] = s
		}
	}
	result := make([]session.State, 0, len(best))
	for _, s := range best {
		result = append(result, s)
	}
	return result
}

func moreRelevantNotificationState(a, b session.State) bool {
	if (a.PID > 0) != (b.PID > 0) {
		return a.PID > 0
	}
	if a.LastUpdate != b.LastUpdate {
		return a.LastUpdate.After(b.LastUpdate)
	}
	if a.FileModTime != b.FileModTime {
		return a.FileModTime.After(b.FileModTime)
	}
	return a.Status != session.StatusDone && b.Status == session.StatusDone
}

func shouldNotifyStatusTransition(previous, current session.Status) bool {
	switch current {
	case session.StatusError:
		return previous != session.StatusError
	case session.StatusDone:
		return previous != session.StatusDone
	case session.StatusCompletedAgo:
		return isActiveWorkStatus(previous)
	case session.StatusIdle:
		return isActiveWorkStatus(previous)
	default:
		return false
	}
}

func isNotifiableStatus(status session.Status) bool {
	return status == session.StatusDone || status == session.StatusError || status == session.StatusIdle || status == session.StatusCompletedAgo
}

func isActiveWorkStatus(status session.Status) bool {
	return session.IsActiveWorkStatus(status)
}

func sessionNotificationKey(s session.State) string {
	switch {
	case s.Cwd != "":
		return strings.ToLower(filepath.Clean(s.Cwd))
	case s.FilePath != "":
		return strings.ToLower(filepath.Clean(s.FilePath))
	case s.SessionID != "":
		return strings.ToLower(s.SessionID)
	default:
		return ""
	}
}

func displayProjectName(s session.State) string {
	if s.ProjectName != "" {
		return s.ProjectName
	}
	if s.Cwd != "" {
		return filepath.Base(s.Cwd)
	}
	if s.FilePath != "" {
		return filepath.Base(filepath.Dir(s.FilePath))
	}
	return "unknown project"
}

func notificationForSession(s session.State) notify.Notification {
	project := displayProjectName(s)
	title := fmt.Sprintf("%s completed: %s", providerLabel(s), project)
	switch s.Status {
	case session.StatusError:
		title = fmt.Sprintf("%s error: %s", providerLabel(s), project)
	case session.StatusIdle:
		title = fmt.Sprintf("%s response complete: %s", providerLabel(s), project)
	case session.StatusCompletedAgo:
		title = fmt.Sprintf("%s response complete: %s", providerLabel(s), project)
	}

	prompt := "n/a"
	if s.LastPrompt != "" {
		prompt = truncateNotificationLine(s.LastPrompt, 120)
	}

	response := "n/a"
	if s.LastResponse != "" {
		response = truncateNotificationLine(s.LastResponse, 120)
	}

	message := strings.Join([]string{
		"Project: " + truncateNotificationLine(project, 60),
		"Prompt: " + prompt,
		"Response: " + response,
	}, "\n")

	return notify.Notification{
		Title:   title,
		Message: message,
	}
}

func truncateNotificationLine(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return string(runes[:1])
	}
	return string(runes[:maxLen-1]) + "…"
}

// Render is a test-friendly wrapper: creates a Model with fixed terminal width
// and returns View(). This keeps test assertions working without a running program.
func Render(sessions []session.State, compact bool) string {
	m := Model{
		sessions:       sessions,
		compact:        compact,
		providerFilter: "all",
		expanded:       make(map[int]bool),
		termW:          120,
		termH:          40,
	}
	sortSessions(m.sessions)
	return m.View()
}

func renderRow(s session.State, now time.Time, c cols, isCursor, isMarked, isKilling bool) string {
	dur := ""
	if !s.StartTime.IsZero() {
		dur = session.FormatDuration(now.Sub(s.StartTime))
	}
	action := actionForStatus(s, now)
	if isKilling {
		action = "Killing…"
	}
	pidStr := ""
	if s.PID > 0 {
		pidStr = fmt.Sprintf("%d", s.PID)
	}

	// Two-char gutter precedes the PID value: a cursor caret and a mark glyph.
	cursorCh := " "
	if isCursor {
		cursorCh = ">"
	}
	markCh := " "
	if isMarked {
		markCh = "*"
	}
	pidW := max(1, c.pid-2)
	pidCell := cursorStyle.Render(cursorCh) + markStyle.Render(markCh) +
		pidStyle.Width(pidW).Render(truncate(pidStr, pidW))

	cells := []string{
		pidCell,
		styledProvider(providerLabel(s), c.provider),
		projectStyle.Width(c.project).Render(truncate(s.ProjectName, c.project)),
		styledStatusCell(s, now, c.status, isKilling),
		actionStyle.Width(c.action).Render(truncate(action, c.action)),
		durationStyle.Width(c.dur).Render(truncate(dur, c.dur)),
	}

	if c.tmux > 0 {
		var tmuxCell string
		if s.TmuxSession == "" {
			tmuxCell = durationStyle.Width(c.tmux).Render(truncate("not in tmux", c.tmux))
		} else {
			tmuxCell = tmuxStyle.Width(c.tmux).Render(truncate(s.TmuxSession, c.tmux))
		}
		cells = append(cells[:1], append([]string{tmuxCell}, cells[1:]...)...)
	}

	return joinCols(cells)
}

var killingStatusStyle = lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("0")).Bold(true)

func styledStatusCell(s session.State, now time.Time, width int, isKilling bool) string {
	if isKilling {
		return killingStatusStyle.Width(width).Render(truncate("KILLING", width))
	}
	return styledStatus(s, now, width)
}

func styledStatus(s session.State, now time.Time, width int) string {
	style, ok := statusStyles[s.Status]
	if !ok {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("0"))
	}
	return style.Width(width).Render(truncate(statusLabel(s, now), width))
}

func statusLabel(s session.State, now time.Time) string {
	switch s.Status {
	case session.StatusCompletedAgo, session.StatusDone:
		return "Done"
	default:
		return string(s.Status)
	}
}

func styledProvider(provider string, width int) string {
	style, ok := providerStyles[provider]
	if !ok {
		style = providerStyles["UNKNOWN"]
	}
	return style.Width(width).Render(truncate(provider, width))
}

func joinCols(cells []string) string {
	sep := separatorStyle.Render(" │ ")
	return strings.Join(cells, sep)
}

func totalWidth(widths []int) int {
	w := 0
	for _, cw := range widths {
		w += cw
	}
	w += (len(widths) - 1) * 3 // " │ " = 3 visible chars each
	return w
}

func hline(width int) string {
	return separatorStyle.Render(strings.Repeat("─", width))
}

// actionForStatus returns the action text appropriate for the session's status.
// Only show the current action when Claude is actively working.
func actionForStatus(s session.State, now time.Time) string {
	switch s.Status {
	case session.StatusThinking:
		return "Thinking..."
	case session.StatusToolUse:
		if s.CurrentAction != "" {
			return s.CurrentAction
		}
		return "Executing tool..."
	case session.StatusStreaming:
		return "Streaming response..."
	case session.StatusResponding:
		if s.CurrentAction != "" {
			return s.CurrentAction
		}
		return "Processing..."
	case session.StatusDone:
		return "Finished"
	case session.StatusCompletedAgo:
		return completedAgoAction(s, now)
	case session.StatusInterrupted:
		return "Interrupted by user"
	case session.StatusWaiting:
		return "Waiting for first prompt..."
	default:
		return ""
	}
}

func completedAgoAction(s session.State, now time.Time) string {
	if s.CompletedAt.IsZero() {
		return "Finished"
	}
	elapsed := now.Sub(s.CompletedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("Finished %s ago", session.FormatDuration(elapsed))
}

func statusPriority(s session.Status) int {
	switch s {
	case session.StatusThinking:
		return 0
	case session.StatusToolUse:
		return 0
	case session.StatusStreaming:
		return 0
	case session.StatusResponding:
		return 0
	case session.StatusError:
		return 1
	case session.StatusInterrupted:
		return 2
	case session.StatusIdle:
		return 3
	case session.StatusCompletedAgo:
		return 3
	case session.StatusWaiting:
		return 4
	case session.StatusDone:
		return 5
	default:
		return 6
	}
}

func providerLabel(s session.State) string {
	if strings.EqualFold(s.Provider, "claude") {
		return "CLAUDE"
	}
	if strings.EqualFold(s.Provider, "copilot") {
		return "COPILOT"
	}
	lowerPath := strings.ToLower(s.FilePath)
	if strings.Contains(lowerPath, string(filepath.Separator)+".copilot"+string(filepath.Separator)+"session-state"+string(filepath.Separator)) ||
		strings.Contains(lowerPath, "/.copilot/session-state/") ||
		strings.Contains(lowerPath, `\.copilot\session-state\`) {
		return "COPILOT"
	}
	if s.FilePath != "" {
		return "CLAUDE"
	}
	return "UNKNOWN"
}

func providerRank(s session.State) int {
	switch providerLabel(s) {
	case "CLAUDE":
		return 0
	case "COPILOT":
		return 1
	default:
		return 2
	}
}

func sortSessions(sessions []session.State) {
	sort.Slice(sessions, func(i, j int) bool {
		ri := providerRank(sessions[i])
		rj := providerRank(sessions[j])
		if ri != rj {
			return ri < rj
		}
		return sessions[i].PID < sessions[j].PID
	})
}

func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func filterSessions(sessions []session.State, provider string) []session.State {
	if provider == "" || strings.EqualFold(provider, "all") {
		return sessions
	}
	filtered := make([]session.State, 0, len(sessions))
	target := strings.ToUpper(strings.TrimSpace(provider))
	for _, s := range sessions {
		if providerLabel(s) == target {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
