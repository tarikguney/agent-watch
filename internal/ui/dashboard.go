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
	helpKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#6CB6FF")) // Soft blue for keys
	helpTextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))                // Gray for descriptions
	providerStyles = map[string]lipgloss.Style{
		"CLAUDE":  lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8800")).Bold(true),
		"COPILOT": lipgloss.NewStyle().Foreground(lipgloss.Color("#6CB6FF")).Bold(true),
		"UNKNOWN": lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}

	statusStyles = map[session.Status]lipgloss.Style{
		session.StatusThinking:    lipgloss.NewStyle().Background(lipgloss.Color("#D4A017")).Foreground(lipgloss.Color("0")).Bold(true), // Amber bg
		session.StatusToolUse:     lipgloss.NewStyle().Background(lipgloss.Color("10")).Foreground(lipgloss.Color("0")).Bold(true),      // Green bg
		session.StatusStreaming:   lipgloss.NewStyle().Background(lipgloss.Color("14")).Foreground(lipgloss.Color("0")).Bold(true),      // Cyan bg
		session.StatusResponding:  lipgloss.NewStyle().Background(lipgloss.Color("10")).Foreground(lipgloss.Color("0")).Bold(true),      // Green bg (fallback)
		session.StatusIdle:        lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("15")),               // Gray bg
		session.StatusDone:        lipgloss.NewStyle().Background(lipgloss.Color("12")).Foreground(lipgloss.Color("15")).Bold(true),     // Blue bg
		session.StatusError:       lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("15")).Bold(true),      // Red bg
		session.StatusInterrupted: lipgloss.NewStyle().Background(lipgloss.Color("11")).Foreground(lipgloss.Color("0")).Bold(true),      // Yellow bg
		session.StatusWaiting:     lipgloss.NewStyle().Background(lipgloss.Color("13")).Foreground(lipgloss.Color("0")).Bold(true),      // Magenta bg
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
		status:   len("Interrupted") + 2, // widest possible status
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
	return Model{
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
	}
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
		switch msg.String() {
		case "up", "k":
			if m.cursorIdx > 0 {
				m.cursorIdx--
			}
		case "down", "j":
			if m.cursorIdx < len(m.sessions)-1 {
				m.cursorIdx++
			}
		case "enter", " ":
			if m.cursorIdx < len(m.sessions) {
				pid := m.sessions[m.cursorIdx].PID
				m.expanded[pid] = !m.expanded[pid]
			}
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
	}
	return m, nil
}

func (m Model) View() string {
	now := time.Now()
	c := computeCols(m.sessions, now, m.termW)
	var b strings.Builder

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
	titleParts = append(titleParts, timestamp)
	b.WriteString(strings.Join(titleParts, " ") + "\n")

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
	b.WriteString(hline(tw) + "\n")
	b.WriteString(joinCols(headers) + "\n")
	b.WriteString(hline(tw) + "\n")

	for i, s := range m.sessions {
		isCursor := i == m.cursorIdx
		isExpanded := m.expanded[s.PID]

		b.WriteString(renderRow(s, now, c, isCursor, m.killing[s.PID]) + "\n")

		if !m.compact && isExpanded {
			prompt := s.LastPrompt
			if prompt == "" {
				prompt = s.OriginalTask
			}
			if prompt != "" {
				prefix := "  » prompt: "
				maxLen := max(1, m.termW-len(prefix))
				b.WriteString(
					durationStyle.Render(prefix) +
						promptStyle.Render(truncate(prompt, maxLen)) + "\n",
				)
			}
			if s.LastResponse != "" {
				prefix := "  » response: "
				maxLen := max(1, m.termW-len(prefix))
				b.WriteString(
					durationStyle.Render(prefix) +
						responseStyle.Render(truncate(s.LastResponse, maxLen)) + "\n",
				)
			}
		}

		if i < len(m.sessions)-1 {
			b.WriteString(hline(tw) + "\n")
		}
	}

	if len(m.sessions) == 0 {
		b.WriteString(durationStyle.Render("  No active sessions found. Watching for new sessions...") + "\n")
	}

	// Help bar or status message (mutually exclusive to keep line count stable)
	if !m.compact {
		b.WriteString("\n" + hline(tw) + "\n")
		if m.killConfirmPID != 0 {
			warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true) // red
			b.WriteString(warnStyle.Render(fmt.Sprintf("  Kill PID %d (%s)? Press y to confirm, any other key to cancel.", m.killConfirmPID, m.killConfirmLabel)))
		} else if m.statusMsg != "" && time.Now().Before(m.statusExp) {
			warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
			b.WriteString(warnStyle.Render("  " + m.statusMsg))
		} else {
			b.WriteString(
				helpKeyStyle.Render("↑↓") + helpTextStyle.Render(" Navigate  ") +
					helpKeyStyle.Render("Enter") + helpTextStyle.Render(" Toggle  ") +
					helpKeyStyle.Render("g") + helpTextStyle.Render(" Go to Window  ") +
					m.notificationHelp() +
					helpKeyStyle.Render("a/l/p") + helpTextStyle.Render(" Filter  ") +
					helpKeyStyle.Render("e") + helpTextStyle.Render(" Expand All  ") +
					helpKeyStyle.Render("c") + helpTextStyle.Render(" Collapse All  ") +
					helpKeyStyle.Render("x") + helpTextStyle.Render(" Kill  ") +
					helpKeyStyle.Render("q") + helpTextStyle.Render(" Quit"),
			)
		}
	}

	return b.String()
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
	case session.StatusIdle:
		return isActiveWorkStatus(previous)
	default:
		return false
	}
}

func isNotifiableStatus(status session.Status) bool {
	return status == session.StatusDone || status == session.StatusError || status == session.StatusIdle
}

func isActiveWorkStatus(status session.Status) bool {
	return status == session.StatusThinking ||
		status == session.StatusToolUse ||
		status == session.StatusStreaming ||
		status == session.StatusResponding
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

func renderRow(s session.State, now time.Time, c cols, isCursor, isKilling bool) string {
	dur := ""
	if !s.StartTime.IsZero() {
		dur = session.FormatDuration(now.Sub(s.StartTime))
	}
	action := actionForStatus(s)
	if isKilling {
		action = "Killing…"
	}
	pidStr := ""
	if s.PID > 0 {
		pidStr = fmt.Sprintf("%d", s.PID)
	}

	// Cursor indicator occupies 2 chars (">" + " "); remaining width goes to PID value.
	var pidCell string
	if isCursor {
		pidW := max(1, c.pid-2)
		pidCell = cursorStyle.Render(">") + " " + pidStyle.Width(pidW).Render(truncate(pidStr, pidW))
	} else {
		pidCell = pidStyle.Width(c.pid).Render(truncate(pidStr, c.pid))
	}

	cells := []string{
		pidCell,
		styledProvider(providerLabel(s), c.provider),
		projectStyle.Width(c.project).Render(truncate(s.ProjectName, c.project)),
		styledStatusCell(s.Status, c.status, isKilling),
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

var killingStatusStyle = lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("15")).Bold(true)

func styledStatusCell(status session.Status, width int, isKilling bool) string {
	if isKilling {
		return killingStatusStyle.Width(width).Render(truncate("KILLING", width))
	}
	return styledStatus(status, width)
}

func styledStatus(status session.Status, width int) string {
	style, ok := statusStyles[status]
	if !ok {
		style = lipgloss.NewStyle()
	}
	return style.Width(width).Render(truncate(string(status), width))
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
func actionForStatus(s session.State) string {
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
		return "Completed"
	case session.StatusInterrupted:
		return "Interrupted by user"
	case session.StatusWaiting:
		return "Waiting for first prompt..."
	default:
		return ""
	}
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
