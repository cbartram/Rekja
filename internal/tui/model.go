package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cbartram/rekja/internal/manifest"

	"github.com/cbartram/rekja/internal/app"
	"github.com/cbartram/rekja/internal/resolve"
	syncengine "github.com/cbartram/rekja/internal/sync"
)

type view int

const (
	viewInstalled view = iota
	viewUpdates
	viewDependencies
	viewSync
	viewLogs
	viewConfig
)

// --- Theme -----------------------------------------------------------------

var (
	colorAccent = lipgloss.Color("39")  // cyan/blue
	colorGood   = lipgloss.Color("42")  // green
	colorWarn   = lipgloss.Color("214") // amber
	colorBad    = lipgloss.Color("203") // red
	colorMuted  = lipgloss.Color("244") // grey
	colorFaint  = lipgloss.Color("237") // border grey
	colorText   = lipgloss.Color("252")

	appStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("0")).
		Background(colorAccent).
		Padding(0, 1)

	subtitleStyle = mutedStyle

	tabActiveStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("0")).
		Background(colorAccent).
		Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
		Foreground(colorMuted).
		Padding(0, 1)

	panelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(1, 2)

	errorStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBad).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBad).
		Padding(0, 1)

	mutedStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	goodStyle     = lipgloss.NewStyle().Foreground(colorGood)
	warnStyle     = lipgloss.NewStyle().Foreground(colorWarn)
	badStyle      = lipgloss.NewStyle().Foreground(colorBad)
	headingStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorText).MarginBottom(1)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
)

// --- Keymap (for help bubble) -----------------------------------------------

type keyMap struct {
	Tab1, Tab2, Tab3, Tab4, Tab5, Tab6 key.Binding
	Up, Down                           key.Binding
	Refresh, Plan, Sync, Logs, Restart key.Binding
	Add, Untrack                       key.Binding
	Quit                               key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "installed")),
		Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "updates")),
		Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "dependencies")),
		Tab4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "sync")),
		Tab5:    key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "logs")),
		Tab6:    key.NewBinding(key.WithKeys("6"), key.WithHelp("6", "config")),
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Plan:    key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "plan")),
		Sync:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sync")),
		Logs:    key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Restart: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart")),
		Add:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		Untrack: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "untrack")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tab1, k.Tab2, k.Tab3, k.Tab4, k.Tab5, k.Tab6, k.Add, k.Untrack, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab1, k.Tab2, k.Tab3, k.Tab4, k.Tab5, k.Tab6},
		{k.Up, k.Down, k.Add, k.Untrack},
		{k.Refresh, k.Plan, k.Sync, k.Logs, k.Restart, k.Quit},
	}
}

// modItem adapts a tracked mod into a list.Item for bubbles/list.
type modItem struct {
	title, desc string
}

func (i modItem) Title() string       { return i.title }
func (i modItem) Description() string { return i.desc }
func (i modItem) FilterValue() string { return i.title }

// Model is the BubbleTea root model.
type Model struct {
	ctx        context.Context
	service    *app.Service
	spinner    spinner.Model
	view       view
	loading    bool
	state      app.State
	plan       resolve.Plan
	list       list.Model
	adding     bool
	addInput   textinput.Model
	logView    viewport.Model
	help       help.Model
	keys       keyMap
	width      int
	height     int
	syncEvents <-chan syncengine.Event
	syncLog    []string
	serverLog  string
	err        error
}

type stateLoadedMsg struct {
	state app.State
	err   error
}

type planBuiltMsg struct {
	plan resolve.Plan
	err  error
}

type syncEventMsg syncengine.Event

type logsLoadedMsg struct {
	logs string
	err  error
}

type restartMsg struct {
	output string
	err    error
}

type trackChangedMsg struct {
	err error
}

// NewModel creates the root TUI model.
func NewModel(ctx context.Context, service *app.Service) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)

	input := textinput.New()
	input.Placeholder = "Namespace/Name[@version]"
	input.CharLimit = 160
	input.SetWidth(48)
	input.Prompt = "› "

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(colorAccent).BorderLeftForeground(colorAccent)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(colorAccent).BorderLeftForeground(colorAccent)
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Installed Mods"
	l.Styles.Title = titleStyle
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)

	vp := viewport.New()

	h := help.New()

	return Model{
		ctx:      ctx,
		service:  service,
		spinner:  sp,
		view:     viewInstalled,
		loading:  true,
		addInput: input,
		list:     l,
		logView:  vp,
		help:     h,
		keys:     defaultKeyMap(),
		width:    100,
		height:   30,
	}
}

// Init starts the initial inventory and remote check.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadState())
}

// Update handles user input and async workflow messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.applySizes()
		return m, nil
	case tea.KeyPressMsg:
		if m.adding {
			return m.updateAddInput(msg)
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "1":
			m.view = viewInstalled
		case "2":
			m.view = viewUpdates
		case "3":
			m.view = viewDependencies
		case "4":
			m.view = viewSync
		case "5":
			m.view = viewLogs
		case "6":
			m.view = viewConfig
		case "r":
			m.loading = true
			return m, m.loadState()
		case "p":
			m.view = viewDependencies
			return m, m.buildPlan()
		case "s":
			m.view = viewSync
			cmd, err := m.startSync()
			if err != nil {
				m.err = err
				return m, nil
			}
			return m, cmd
		case "l":
			m.view = viewLogs
			return m, m.loadLogs()
		case "R":
			m.view = viewLogs
			return m, m.restart()
		case "a":
			m.view = viewConfig
			m.adding = true
			m.addInput.SetValue("")
			m.addInput.Focus()
			return m, textinput.Blink
		case "d":
			if m.view == viewConfig || m.view == viewInstalled {
				return m, m.untrackSelected()
			}
		case "up", "k", "down", "j":
			if m.view == viewInstalled {
				var cmd tea.Cmd
				m.list, cmd = m.list.Update(msg)
				return m, cmd
			}
		}
		if m.view == viewLogs {
			var cmd tea.Cmd
			m.logView, cmd = m.logView.Update(msg)
			return m, cmd
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case stateLoadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.state = msg.state
			m.list.SetItems(trackedToItems(m.state.Inventory.Manifest.Tracked))
		}
	case planBuiltMsg:
		m.err = msg.err
		if msg.err == nil {
			m.plan = msg.plan
		}
	case syncEventMsg:
		event := syncengine.Event(msg)
		if event.Err != nil {
			m.err = event.Err
			m.syncEvents = nil
		}
		if event.Package != "" || event.Message != "" {
			m.syncLog = append(m.syncLog, fmt.Sprintf("%s %s", event.Package, event.Message))
		}
		if event.Done {
			m.syncEvents = nil
			return m, m.loadState()
		}
		if m.syncEvents != nil {
			return m, m.waitForSyncEvent()
		}
	case logsLoadedMsg:
		m.err = msg.err
		m.serverLog = msg.logs
		m.logView.SetContent(msg.logs)
		m.logView.GotoBottom()
	case restartMsg:
		m.err = msg.err
		if msg.output != "" {
			m.serverLog = msg.output
			m.logView.SetContent(msg.output)
			m.logView.GotoBottom()
		}
	case trackChangedMsg:
		m.err = msg.err
		if msg.err == nil {
			return m, m.loadState()
		}
	}
	return m, nil
}

// applySizes recalculates inner component dimensions from the terminal size.
func (m *Model) applySizes() {
	innerW := m.width - 6 // account for appStyle/panelStyle padding+border
	if innerW < 20 {
		innerW = 20
	}
	innerH := m.height - 12
	if innerH < 5 {
		innerH = 5
	}
	m.list.SetSize(innerW, innerH)
	m.logView.SetWidth(innerW)
	m.logView.SetHeight(innerH)
}

func trackedToItems(tracked []manifest.TrackedMod) []list.Item {
	items := make([]list.Item, len(tracked))
	for i, mod := range tracked {
		status := goodStyle.Render("✓ up to date")
		if mod.DesiredVersion != "" && mod.DesiredVersion != mod.InstalledVersion {
			status = warnStyle.Render(fmt.Sprintf("→ %s", mod.DesiredVersion))
		}
		items[i] = modItem{
			title: mod.Key(),
			desc:  fmt.Sprintf("installed %s  %s", mod.InstalledVersion, status),
		}
	}
	return items
}

// View renders the active screen.
func (m Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

var rekjaBanner = []string{
	`▛▀▖▞▀▖▌  ▐▌▝▀▖`,
	`▙▄▘▛▀ ▙▄▌▐▌▞▀▌`,
	`▌▝▖▙▄▖▌ ▌▐▌▝▄▌`,
}

func (m Model) render() string {
	var b strings.Builder

	bannerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	for _, line := range rekjaBanner {
		b.WriteString(bannerStyle.Render(line))
		b.WriteString("\n")
	}
	subtitle := subtitleStyle.Render("Valheim mod manager")
	if m.loading {
		subtitle += "  " + m.spinner.View()
	}
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	ruleWidth := m.width - 4
	if ruleWidth < 1 {
		ruleWidth = 1
	}
	b.WriteString(lipgloss.NewStyle().Foreground(colorFaint).Render(strings.Repeat("─", ruleWidth)))
	b.WriteString("\n\n")
	b.WriteString(m.nav())
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("✕ " + m.err.Error()))
		b.WriteString("\n\n")
	}

	var body string
	switch m.view {
	case viewInstalled:
		body = m.installedView()
	case viewUpdates:
		body = m.updatesView()
	case viewDependencies:
		body = m.dependenciesView()
	case viewSync:
		body = m.syncView()
	case viewLogs:
		body = m.logsView()
	case viewConfig:
		body = m.configView()
	}
	b.WriteString(panelStyle.Width(m.width - 4).Render(body))
	b.WriteString("\n\n")
	b.WriteString(m.help.View(m.keys))

	return appStyle.Render(b.String())
}

func (m Model) nav() string {
	labels := []string{"Installed", "Updates", "Dependencies", "Sync", "Logs", "Config"}
	parts := make([]string, len(labels))
	for index, label := range labels {
		if view(index) == m.view {
			parts[index] = tabActiveStyle.Render(label)
		} else {
			parts[index] = tabInactiveStyle.Render(label)
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) installedView() string {
	var b strings.Builder
	if len(m.state.Inventory.Manifest.Tracked) == 0 {
		b.WriteString(mutedStyle.Render("No tracked mods installed yet. Press a to add one."))
	} else {
		b.WriteString(m.list.View())
	}

	if len(m.state.Inventory.UntrackedFiles) > 0 {
		b.WriteString("\n\n")
		b.WriteString(headingStyle.Render("Untracked files"))
		b.WriteString("\n")
		for _, file := range m.state.Inventory.UntrackedFiles {
			b.WriteString(mutedStyle.Render(fmt.Sprintf("  • %s (%s)\n", file.Path, file.Kind)))
		}
	}
	if len(m.state.Inventory.TrackedDrift) > 0 {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("Drift detected"))
		b.WriteString("\n")
		for _, file := range m.state.Inventory.TrackedDrift {
			b.WriteString(warnStyle.Render(fmt.Sprintf("  • %s\n", file.Path)))
		}
	}
	return b.String()
}

func (m Model) updatesView() string {
	if len(m.state.Updates) == 0 {
		return goodStyle.Render("✓ Everything is up to date.")
	}
	var b strings.Builder
	b.WriteString(headingStyle.Render(fmt.Sprintf("%d update(s) available", len(m.state.Updates))))
	b.WriteString("\n")
	for _, update := range m.state.Updates {
		b.WriteString(fmt.Sprintf("  %s  %s %s %s\n",
			selectedStyle.Render(update.Package.Key()),
			mutedStyle.Render(update.InstalledVersion),
			mutedStyle.Render("→"),
			warnStyle.Render(update.LatestVersion),
		))
	}
	return b.String()
}

func (m Model) dependenciesView() string {
	var b strings.Builder
	if len(m.plan.Roots) == 0 && len(m.plan.Dependencies) == 0 {
		b.WriteString(mutedStyle.Render("No sync plan loaded. Press p to resolve dependencies."))
		return b.String()
	}
	b.WriteString(headingStyle.Render("Roots"))
	b.WriteString("\n")
	for _, item := range m.plan.Roots {
		b.WriteString(fmt.Sprintf("  %s %s\n", selectedStyle.Render(item.Package.FullName), mutedStyle.Render(item.Version.VersionNumber)))
	}
	b.WriteString("\n")
	b.WriteString(headingStyle.Render("Dependencies"))
	b.WriteString("\n")
	if len(m.plan.Dependencies) == 0 {
		b.WriteString(mutedStyle.Render("  none\n"))
	}
	for _, item := range m.plan.Dependencies {
		b.WriteString(fmt.Sprintf("  %s %s %s\n",
			item.Package.FullName,
			mutedStyle.Render(item.Version.VersionNumber),
			mutedStyle.Render("required by "+item.DependencyOf),
		))
	}
	if len(m.plan.Warnings) > 0 {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render("Warnings"))
		b.WriteString("\n")
		for _, warning := range m.plan.Warnings {
			b.WriteString(warnStyle.Render("  • " + warning + "\n"))
		}
	}
	return b.String()
}

func (m Model) syncView() string {
	if len(m.syncLog) == 0 {
		return mutedStyle.Render("No sync has run. Press s to apply the current plan.")
	}
	var b strings.Builder
	for _, line := range m.syncLog {
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func (m Model) logsView() string {
	if m.serverLog == "" {
		return mutedStyle.Render("No logs loaded. Press l for logs or R to restart the server.")
	}
	return m.logView.View()
}

func (m Model) configView() string {
	cfg := m.service.Config()
	var b strings.Builder
	b.WriteString(headingStyle.Render("Configuration"))
	b.WriteString("\n")
	rows := [][2]string{
		{"Plugins", cfg.PluginsDir},
		{"Manifest", cfg.ManifestPath()},
		{"Thunderstore", cfg.ThunderstoreBaseURL},
		{"Namespace", cfg.Kubernetes.Namespace},
		{"Pod", cfg.Kubernetes.PodName},
		{"Selector", cfg.Kubernetes.LabelSelector},
		{"Container", cfg.Kubernetes.ContainerName},
	}
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("  %s %s\n", mutedStyle.Width(14).Render(row[0]+":"), row[1]))
	}

	b.WriteString("\n")
	b.WriteString(headingStyle.Render("Tracked mods"))
	b.WriteString("\n")
	if len(m.state.Inventory.Manifest.Tracked) == 0 {
		b.WriteString(mutedStyle.Render("  None\n"))
	}

	for _, tracked := range m.state.Inventory.Manifest.Tracked {
		b.WriteString(fmt.Sprintf("  %s desired=%s installed=%s\n",
			tracked.Key(), mutedStyle.Render(tracked.DesiredVersion), mutedStyle.Render(tracked.InstalledVersion)))
	}

	if m.adding {
		b.WriteString("\n")
		b.WriteString(headingStyle.Render("Add tracked mod"))
		b.WriteString("\n  ")
		b.WriteString(m.addInput.View())
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) loadState() tea.Cmd {
	return func() tea.Msg {
		state, err := m.service.LoadState(m.ctx)
		return stateLoadedMsg{state: state, err: err}
	}
}

func (m Model) buildPlan() tea.Cmd {
	return func() tea.Msg {
		plan, err := m.service.BuildSyncPlan(m.state)
		return planBuiltMsg{plan: plan, err: err}
	}
}

func (m *Model) startSync() (tea.Cmd, error) {
	plan := m.plan
	if len(plan.Roots) == 0 && len(plan.Dependencies) == 0 {
		var err error
		plan, err = m.service.BuildSyncPlan(m.state)
		if err != nil {
			return nil, err
		}
		m.plan = plan
	}
	events := make(chan syncengine.Event)
	m.syncEvents = events
	m.syncLog = append(m.syncLog, "sync started")
	go func() {
		if err := m.service.ApplySync(m.ctx, plan, events); err != nil {
			return
		}
	}()
	return m.waitForSyncEvent(), nil
}

func (m Model) waitForSyncEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.syncEvents
		if !ok {
			return syncEventMsg{Message: "sync complete", Done: true}
		}
		return syncEventMsg(event)
	}
}

func (m Model) loadLogs() tea.Cmd {
	return func() tea.Msg {
		logs, err := m.service.Logs(m.ctx, m.state.Target, 200)
		return logsLoadedMsg{logs: logs, err: err}
	}
}

func (m Model) restart() tea.Cmd {
	return func() tea.Msg {
		output, err := m.service.RestartServer(m.ctx, m.state.Target)
		return restartMsg{output: output, err: err}
	}
}

func (m Model) updateAddInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.adding = false
		m.addInput.Blur()
		return m, nil
	case "enter":
		value := m.addInput.Value()
		m.adding = false
		m.addInput.Blur()
		return m, func() tea.Msg {
			return trackChangedMsg{err: m.service.TrackMod(value)}
		}
	}
	var cmd tea.Cmd
	m.addInput, cmd = m.addInput.Update(msg)
	return m, cmd
}

func (m Model) untrackSelected() tea.Cmd {
	tracked := m.state.Inventory.Manifest.Tracked
	if len(tracked) == 0 {
		return nil
	}
	index := m.list.Index()
	if index < 0 || index >= len(tracked) {
		return nil
	}
	fullName := tracked[index].Key()
	return func() tea.Msg {
		return trackChangedMsg{err: m.service.UntrackMod(fullName)}
	}
}
