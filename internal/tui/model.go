package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// Model is the BubbleTea root model.
type Model struct {
	ctx        context.Context
	service    *app.Service
	spinner    spinner.Model
	view       view
	loading    bool
	state      app.State
	plan       resolve.Plan
	selected   int
	adding     bool
	addInput   textinput.Model
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
	input := textinput.New()
	input.Placeholder = "Namespace-Name[@version]"
	input.CharLimit = 160
	input.SetWidth(48)
	return Model{
		ctx:      ctx,
		service:  service,
		spinner:  sp,
		view:     viewInstalled,
		loading:  true,
		addInput: input,
	}
}

// Init starts the initial inventory and remote check.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadState())
}

// Update handles user input and async workflow messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
		case "up", "k":
			m.moveSelection(-1)
		case "down", "j":
			m.moveSelection(1)
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
			if m.selected >= len(m.state.Inventory.Manifest.Tracked) {
				m.selected = 0
			}
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
	case restartMsg:
		m.err = msg.err
		if msg.output != "" {
			m.serverLog = msg.output
		}
	case trackChangedMsg:
		m.err = msg.err
		if msg.err == nil {
			return m, m.loadState()
		}
	}
	return m, nil
}

// View renders the active screen.
func (m Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m Model) render() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Rekja"))
	if m.loading {
		builder.WriteString(" ")
		builder.WriteString(m.spinner.View())
	}
	builder.WriteString("\n")
	builder.WriteString(m.nav())
	builder.WriteString("\n\n")
	if m.err != nil {
		builder.WriteString(errorStyle.Render(m.err.Error()))
		builder.WriteString("\n\n")
	}

	switch m.view {
	case viewInstalled:
		builder.WriteString(m.installedView())
	case viewUpdates:
		builder.WriteString(m.updatesView())
	case viewDependencies:
		builder.WriteString(m.dependenciesView())
	case viewSync:
		builder.WriteString(m.syncView())
	case viewLogs:
		builder.WriteString(m.logsView())
	case viewConfig:
		builder.WriteString(m.configView())
	}
	builder.WriteString("\n\n")
	builder.WriteString(mutedStyle.Render("1 installed  2 updates  3 dependencies  4 sync  5 logs  6 config  a add  d untrack  r refresh  p plan  s sync  l logs  R restart  q quit"))
	return builder.String()
}

func (m Model) nav() string {
	labels := []string{"Installed", "Updates", "Dependencies", "Sync", "Logs", "Config"}
	parts := make([]string, len(labels))
	for index, label := range labels {
		if view(index) == m.view {
			parts[index] = "[" + label + "]"
		} else {
			parts[index] = label
		}
	}
	return strings.Join(parts, "  ")
}

func (m Model) installedView() string {
	var builder strings.Builder
	if len(m.state.Inventory.Manifest.Tracked) == 0 {
		builder.WriteString("No tracked mods installed yet.\n")
	} else {
		for index, mod := range m.state.Inventory.Manifest.Tracked {
			marker := " "
			if index == m.selected {
				marker = ">"
			}
			builder.WriteString(fmt.Sprintf("%s %s %s\n", marker, mod.Key(), mod.InstalledVersion))
		}
	}
	if len(m.state.Inventory.UntrackedFiles) > 0 {
		builder.WriteString("\nUntracked files:\n")
		for _, file := range m.state.Inventory.UntrackedFiles {
			builder.WriteString(fmt.Sprintf("- %s %s\n", file.Path, file.Kind))
		}
	}
	if len(m.state.Inventory.TrackedDrift) > 0 {
		builder.WriteString("\nDrift detected:\n")
		for _, file := range m.state.Inventory.TrackedDrift {
			builder.WriteString(fmt.Sprintf("- %s\n", file.Path))
		}
	}
	return builder.String()
}

func (m Model) updatesView() string {
	if len(m.state.Updates) == 0 {
		return "No updates detected.\n"
	}
	var builder strings.Builder
	for _, update := range m.state.Updates {
		builder.WriteString(fmt.Sprintf("%s %s -> %s\n", update.Package.Key(), update.InstalledVersion, update.LatestVersion))
	}
	return builder.String()
}

func (m Model) dependenciesView() string {
	var builder strings.Builder
	if len(m.plan.Roots) == 0 && len(m.plan.Dependencies) == 0 {
		builder.WriteString("No sync plan loaded. Press p to resolve dependencies.\n")
		return builder.String()
	}
	builder.WriteString("Roots:\n")
	for _, item := range m.plan.Roots {
		builder.WriteString(fmt.Sprintf("- %s %s\n", item.Package.FullName, item.Version.VersionNumber))
	}
	builder.WriteString("\nDependencies:\n")
	if len(m.plan.Dependencies) == 0 {
		builder.WriteString("- none\n")
	}
	for _, item := range m.plan.Dependencies {
		builder.WriteString(fmt.Sprintf("- %s %s required by %s\n", item.Package.FullName, item.Version.VersionNumber, item.DependencyOf))
	}
	if len(m.plan.Warnings) > 0 {
		builder.WriteString("\nWarnings:\n")
		for _, warning := range m.plan.Warnings {
			builder.WriteString("- " + warning + "\n")
		}
	}
	return builder.String()
}

func (m Model) syncView() string {
	if len(m.syncLog) == 0 {
		return "No sync has run. Press s to apply the current plan.\n"
	}
	return strings.Join(m.syncLog, "\n") + "\n"
}

func (m Model) logsView() string {
	if m.serverLog == "" {
		return "No logs loaded. Press l for logs or R to restart the server.\n"
	}
	return m.serverLog
}

func (m Model) configView() string {
	cfg := m.service.Config()
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Plugins: %s\nManifest: %s\nThunderstore: %s\nKubernetes: namespace=%s pod=%s selector=%s container=%s\n",
		cfg.PluginsDir,
		cfg.ManifestPath(),
		cfg.ThunderstoreBaseURL,
		cfg.Kubernetes.Namespace,
		cfg.Kubernetes.PodName,
		cfg.Kubernetes.LabelSelector,
		cfg.Kubernetes.ContainerName,
	))
	builder.WriteString("\nTracked mods:\n")
	if len(m.state.Inventory.Manifest.Tracked) == 0 {
		builder.WriteString("- none\n")
	}
	for index, tracked := range m.state.Inventory.Manifest.Tracked {
		marker := " "
		if index == m.selected {
			marker = ">"
		}
		builder.WriteString(fmt.Sprintf("%s %s desired=%s installed=%s\n", marker, tracked.Key(), tracked.DesiredVersion, tracked.InstalledVersion))
	}
	if m.adding {
		builder.WriteString("\nAdd tracked mod: ")
		builder.WriteString(m.addInput.View())
		builder.WriteString("\n")
	}
	return builder.String()
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

func (m *Model) moveSelection(delta int) {
	count := len(m.state.Inventory.Manifest.Tracked)
	if count == 0 {
		m.selected = 0
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = count - 1
	}
	if m.selected >= count {
		m.selected = 0
	}
}

func (m Model) untrackSelected() tea.Cmd {
	if len(m.state.Inventory.Manifest.Tracked) == 0 || m.selected >= len(m.state.Inventory.Manifest.Tracked) {
		return nil
	}
	fullName := m.state.Inventory.Manifest.Tracked[m.selected].Key()
	return func() tea.Msg {
		return trackChangedMsg{err: m.service.UntrackMod(fullName)}
	}
}
