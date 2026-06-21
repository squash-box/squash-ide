package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/ghx"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/procstat"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/tmux"
	"github.com/squashbox/squash-ide/internal/vault"
)

// lookPath is the seam the /log-task pre-flight uses to verify `claude` is on
// PATH. It is a package var (defaulting to os/exec.LookPath) so tests can drive
// the popover/exec paths without the binary actually installed — the same fake
// seam discipline as internal/exec.Runner.LookPath.
var lookPath = exec.LookPath

// uiDebug gates the native-engine debug trace (focus changes) on SQUASH_DEBUG,
// matching the pane package's gate so a single env var lights up the whole
// native engine's lifecycle logging. Resize traces come from the pane manager
// itself; this covers the UI-side focus toggle.
var uiDebug = os.Getenv("SQUASH_DEBUG") != ""

func uidebugf(format string, args ...any) {
	if !uiDebug {
		return
	}
	fmt.Fprintf(os.Stderr, "squash-ide debug: ui: "+format+"\n", args...)
}

// view represents which screen the user is on.
type view int

const (
	listView view = iota
	detailView
)

// activeIndicator is the glyph used to mark active tasks in the list
// and detail view.
const activeIndicator = "●"

// displayItem is a row in the list: a status header, a task card, or a
// dimmed placeholder shown when a section has no tasks (currently only
// the ACTIVE section, which we always want visible so the user has a
// clear "nothing running" cue and knows how to activate something).
type displayItem struct {
	isHeader      bool
	isPlaceholder bool
	header        string
	placeholder   string
	task          task.Task
}

// selectable reports whether the cursor can land on this item.
func (d displayItem) selectable() bool {
	return !d.isHeader && !d.isPlaceholder
}

// Model is the top-level Bubble Tea model.
type Model struct {
	cfg       config.Config
	vaultPath string // cfg.Vault — cached for rendering

	allTasks []task.Task   // all loaded tasks
	items    []displayItem // grouped display items (headers + tasks)
	filtered []displayItem // items after filter applied
	cursor   int           // index into filtered

	filter       string
	filterActive bool

	view     view
	viewport viewport.Model

	width  int
	height int

	// windowWidth caches the tmux window column count (total terminal width
	// inside tmux), refreshed at the same handler sites that call
	// checkCompactPane. isCompact keys on this — not m.width — because in
	// tmux m.width is the *pane* width, which gets pinned to CompactListWidth
	// once compact engages and would otherwise leave the predicate stuck.
	// Outside tmux this stays 0 and isCompact falls back to m.width.
	windowWidth int

	err error

	resetCursorOnLoad bool              // scroll to top after next tasksLoadedMsg
	tooNarrow         bool              // true when zoomed due to narrow terminal
	formZoomed        bool              // true while pane is zoomed for the new-task form
	compact           bool              // true while pane is shrunk to CompactListWidth
	needsRespawn      bool              // true until the first task load triggers respawn
	RespawnFunc       func([]task.Task) // called once after first load to respawn active panes

	// Native engine (T-038). engineNative gates every native-mode branch;
	// when false the model behaves exactly as the tmux build. manager is the
	// in-process pane authority (nil in tmux mode). paneFocused is the focus
	// owner: when true, keystrokes route to the focused pane's child instead of
	// being interpreted as list commands.
	engineNative bool
	manager      paneManager
	paneFocused  bool
	layoutName   string // active native layout name (T-040); cycled by the 'L' key

	// statsCollector samples per-pane CPU/mem for the header readout (T-055). It
	// is non-nil only when the native engine is on AND cfg.PaneStats is true; a
	// nil collector means the 15s tickResources is never armed, so the feature is
	// a clean no-op when disabled or under tmux. It is a pointer so its retained
	// per-pid baseline survives Model value-copies, and it is touched only from
	// the single outstanding tickResources goroutine (no extra lock needed).
	statsCollector *procstat.Collector

	// logTaskSeq mints unique synthetic task ids (log-task-N) for native-engine
	// /log-task tabs (T-054). Each ctrl+d in the add-task form launches a regular
	// pane keyed by the next id, so concurrent sessions stay distinct for the
	// CloseByTask/DoneByTask auto-close watcher — they are not real vault tasks.
	logTaskSeq int

	// dismissedFocus records task ids whose input_required pane the user
	// deliberately left via ctrl+w (T-048). While a task is in this set,
	// focus-follows-input will not re-surface its *standing* prompt — so the
	// user can stay on the list and spawn more tasks. An entry is cleared the
	// moment the pane makes genuine progress out of input_required (a real new
	// status report, not a transient gap), re-arming surfacing for the next,
	// genuinely new prompt. Keyed by task id.
	dismissedFocus map[string]bool

	// MCP status polling
	subStatuses map[string]status.File // keyed by task ID

	// Lifecycle progress (T-053). prProgress caches the gh-detected PR/CI state
	// per active task, refreshed on the throttled tickProgress poll (gh is
	// network — it must never ride the 1 s tickStatus loop). progressStarted is
	// a one-shot guard so the recurring poll is kicked exactly once, after the
	// first task load. Keyed by task ID.
	prProgress      map[string]ghProgress
	progressStarted bool

	// Dispatch / cleanup state
	confirming   *task.Task   // non-nil when spawn confirmation dialog is showing
	completing   *task.Task   // non-nil when complete confirmation dialog is showing
	deactivating *task.Task   // non-nil when deactivate confirmation dialog is showing
	blocking     *task.Task   // non-nil when block-reason input is active
	blockReason  string       // current text buffer for the block reason
	creatingTask *newTaskForm // non-nil when the new-task form is open
	dispatching  bool         // true while an async op is in progress
	statusMsg    string       // transient message for status bar
	statusIsErr  bool         // whether statusMsg is an error
}

// New creates a new Model from the resolved config. In native-engine mode it
// also constructs the in-process pane manager the right region renders.
func New(cfg config.Config) Model {
	m := Model{
		cfg:            cfg,
		vaultPath:      cfg.Vault,
		needsRespawn:   true,
		dismissedFocus: map[string]bool{},
	}
	if cfg.Engine == config.EngineNative {
		m.engineNative = true
		m.layoutName = cfg.Layout
		m.manager = pane.NewManager(
			pane.WithStrategy(pane.StrategyForName(cfg.Layout)),
		)
		// The header CPU/mem readout (T-055) is opt-out via pane_stats. When on,
		// build a collector over the platform sampler (a /proc reader on Linux, a
		// no-op stub elsewhere — the header simply shows nothing off Linux).
		if cfg.PaneStats {
			m.statsCollector = procstat.NewCollector(procstat.NewSampler())
		}
	}
	return m
}

// NewForTest constructs a Model pre-loaded with tasks and status entries,
// ready to render without the async Init → loadTasks → tasksLoadedMsg
// round-trip. Intended for e2e tests that want to assert the rendered view
// directly; production callers should use New.
func NewForTest(cfg config.Config, tasks []task.Task, statuses map[string]status.File) Model {
	m := New(cfg)
	m.allTasks = tasks
	m.subStatuses = statuses
	m.width = 80
	m.height = 24
	m.needsRespawn = false
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

// Init loads tasks from the vault and starts the status polling ticker. In
// native mode it also begins listening for pane output so the right region
// repaints when a child writes.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadTasks, m.tickStatus()}
	if m.engineNative {
		cmds = append(cmds, m.waitForPaneOutput())
		// Arm the 15s resource tick only when pane stats are enabled (collector
		// non-nil); otherwise the readout is a clean no-op (T-055).
		if rc := m.tickResources(); rc != nil {
			cmds = append(cmds, rc)
		}
	}
	return tea.Batch(cmds...)
}

type tasksLoadedMsg struct {
	tasks []task.Task
	err   error
}

type dispatchDoneMsg struct {
	taskID string
	branch string
	task   task.Task // carried for native spawn-failure rollback

	// native is non-nil under engine=native: the prepared command the handler
	// feeds to manager.Spawn (the pane is launched on the UI goroutine, not in
	// the dispatch goroutine, because the manager lives in the Model).
	native *dispatch.NativeSpawn
}

type dispatchErrMsg struct {
	err error
}

type completeDoneMsg struct {
	taskID string
}

type blockDoneMsg struct {
	taskID string
}

type deactivateDoneMsg struct {
	taskID string
}

type statusTickMsg struct {
	statuses map[string]status.File
	// focusTaskID is set (native engine) when a notify-click left a focus
	// request for the TUI to honour — see status.TakeFocusRequest.
	focusTaskID string
}

// progressTickMsg carries the freshly-polled gh PR/CI state for active tasks
// (T-053). Emitted by tickProgress / pollProgressNow.
type progressTickMsg struct {
	progress map[string]ghProgress
}

// paneOutputMsg is emitted (native mode) when the pane manager signals output
// or a state change. Its only job is to trigger a re-View; the handler
// re-arms waitForPaneOutput for the next signal.
type paneOutputMsg struct{}

// resourceTickMsg carries one round of sampled per-pane CPU/mem usage (T-055),
// keyed by task id. Emitted by the 15s tickResources, independent of the 1s
// status tick.
type resourceTickMsg struct {
	usage map[string]procstat.Usage
}

type logTaskDoneMsg struct{}

type logTaskErrMsg struct {
	err error
}

// logTaskTabDoneMsg is emitted (native engine) when a /log-task tab's claude
// child exits, carrying the synthetic tab id so the handler can auto-close that
// specific pane (T-054). Distinct from logTaskDoneMsg, which is the tmux
// fullscreen path's single completion signal.
type logTaskTabDoneMsg struct {
	taskID string
}

func (m Model) loadTasks() tea.Msg {
	tasks, err := vault.ReadAll(m.vaultPath)
	return tasksLoadedMsg{tasks: tasks, err: err}
}

func (m Model) tickStatus() tea.Cmd {
	engineNative := m.engineNative
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		statuses, _ := status.ReadAll()
		msg := statusTickMsg{statuses: statuses}
		// Native engine: piggyback the notify-click focus request on the same
		// poll the badge sync already runs, so a click brings the right pane
		// forward within a tick (the tmux path does this synchronously via
		// select-pane; here it crosses the process boundary as a file marker).
		if engineNative {
			if id, ok := status.TakeFocusRequest(); ok {
				msg.focusTaskID = id
			}
		}
		return msg
	})
}

// progressPollInterval throttles the gh network poll. gh shells out over the
// network, so it must NOT ride the 1 s tickStatus loop.
const progressPollInterval = 20 * time.Second

// progressProbe is a snapshot of what tickProgress needs to query one task,
// captured at command-arm time (branch + worktree path are pure derivations).
type progressProbe struct {
	id       string
	repoPath string // the task's worktree — shares origin with the main repo
	branch   string
}

// progressProbes snapshots the active tasks to poll. Returns nil when CI polling
// is disabled (cfg.Progress.PollCI=false) so the poll issues no gh call and the
// pr/ci lights stay grey.
func (m Model) progressProbes() []progressProbe {
	if !m.cfg.Progress.PollCI {
		return nil
	}
	var probes []progressProbe
	for _, t := range m.allTasks {
		if t.Status != "active" {
			continue
		}
		wt, err := dispatch.WorktreePathFor(m.cfg, t)
		if err != nil {
			continue
		}
		probes = append(probes, progressProbe{id: t.ID, repoPath: wt, branch: dispatch.BranchFor(t)})
	}
	return probes
}

// runProgressPoll queries gh for each probe and rolls the results into a
// progressTickMsg. A single PRChecksForBranch call per task yields both signals:
// a non-error result means the PR exists (prRaised) and carries the CI rollup;
// ErrNoPR means no PR yet. gh errors are swallowed (degrade to grey, never an
// error badge): a missing PR, absent gh (ErrGHMissing), or any transient failure
// leaves that task's lights grey. Runs inside a tea.Cmd goroutine.
func runProgressPoll(probes []progressProbe) tea.Msg {
	progress := make(map[string]ghProgress, len(probes))
	for _, p := range probes {
		var gp ghProgress
		cs, err := ghx.PRChecksForBranch(p.repoPath, p.branch)
		switch {
		case err == nil:
			gp.prRaised = true // a non-error result means an open PR exists
			gp.checks = cs
		case errors.Is(err, ghx.ErrNoPR):
			gp.checks = ghx.ChecksNone // no PR raised yet — pr/ci stay grey
		default:
			uidebugf("progress poll: %s: %v", p.id, err)
			gp.checks = ghx.ChecksNone
		}
		progress[p.id] = gp
	}
	return progressTickMsg{progress: progress}
}

// scheduleProgress arms a gh poll. delay <= 0 runs it immediately (the one-shot
// kick after the first task load, so the strip lights up without waiting a full
// interval); a positive delay wraps it in a tea.Tick (the recurring throttle).
// Probes are snapshotted now; the progressTickMsg handler re-arms with the
// then-current active set.
func (m Model) scheduleProgress(delay time.Duration) tea.Cmd {
	probes := m.progressProbes()
	if delay <= 0 {
		return func() tea.Msg { return runProgressPoll(probes) }
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return runProgressPoll(probes)
	})
}

// paneStatsInterval is the fixed cadence of the header CPU/mem refresh (T-055).
// It is a package const, not a config key — the requirement is a fixed 15s,
// deliberately decoupled from the 1s badge tick so the resource readout doesn't
// flood /proc.
const paneStatsInterval = 15 * time.Second

// tickResources samples every live pane's process group every 15s and returns a
// resourceTickMsg the Update handler fans out to SetStatsByTask (T-055). It
// snapshots PIDsByTask, then SampleAll outside any lock (the collector is
// touched only here, and only one tick is ever outstanding — re-armed from the
// handler — so no new synchronisation is needed). It returns nil when the
// feature is off (no collector), so a stray call is a harmless no-op.
func (m Model) tickResources() tea.Cmd {
	if !m.engineNative || m.statsCollector == nil {
		return nil
	}
	manager := m.manager
	collector := m.statsCollector
	return tea.Tick(paneStatsInterval, func(time.Time) tea.Msg {
		usage := collector.SampleAll(manager.PIDsByTask())
		return resourceTickMsg{usage: usage}
	})
}

func (m Model) runDispatch(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		res, err := dispatch.Run(cfg, t)
		if err != nil {
			return dispatchErrMsg{err: err}
		}
		return dispatchDoneMsg{taskID: t.ID, branch: res.Branch, task: t, native: res.Native}
	}
}

// runRollback undoes a native dispatch whose pane failed to spawn: it moves the
// just-activated task back to the backlog and removes the worktree (via
// Deactivate, which under engine=native does no pane teardown), then reloads.
// Mirrors the tmux reject-and-kill cleanup (spawner.go:173-176) — no orphan
// active task, no orphan worktree. The local task is stamped active because Run
// already moved the file there.
func (m Model) runRollback(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		t.Status = "active"
		if err := dispatch.Deactivate(cfg, t); err != nil {
			uidebugf("native spawn rollback failed for %s: %v", t.ID, err)
		}
		tasks, err := vault.ReadAll(cfg.Vault)
		return tasksLoadedMsg{tasks: tasks, err: err}
	}
}

func (m Model) runComplete(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Complete(cfg, t); err != nil {
			return dispatchErrMsg{err: err}
		}
		return completeDoneMsg{taskID: t.ID}
	}
}

func (m Model) runBlock(t task.Task, reason string) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Block(cfg, t, reason); err != nil {
			return dispatchErrMsg{err: err}
		}
		return blockDoneMsg{taskID: t.ID}
	}
}

func (m Model) runDeactivate(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Deactivate(cfg, t); err != nil {
			return dispatchErrMsg{err: err}
		}
		return deactivateDoneMsg{taskID: t.ID}
	}
}

// runLogTask hands the form data off to the `/log-task` Claude skill, which
// owns task creation + codebase enrichment. The subprocess runs on the TUI
// pane's TTY via tea.ExecProcess, so the skill can ask clarifying questions
// interactively. On completion we reload the vault — the skill has written
// the new task file, board row, log entry, and entity-page updates itself.
//
// Hard-fails if `claude` isn't on PATH; the form deliberately doesn't fall
// back to a local writer so the single-source-of-truth contract with the
// skill is preserved.
func (m Model) runLogTask(f newTaskForm) tea.Cmd {
	if _, err := lookPath("claude"); err != nil {
		return func() tea.Msg {
			return logTaskErrMsg{err: fmt.Errorf("claude CLI not found on PATH — install it to create tasks")}
		}
	}

	prompt := buildLogTaskPrompt(f)
	cmd := exec.Command("claude", prompt)
	// vault.ExpandHome resolves a leading `~` — the OS's chdir doesn't.
	cmd.Dir = vault.ExpandHome(m.vaultPath)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return logTaskErrMsg{err: fmt.Errorf("/log-task: %w", err)}
		}
		return logTaskDoneMsg{}
	})
}

// spawnLogTaskTab is the native-engine counterpart of runLogTask: instead of
// suspending Bubble Tea and handing the whole terminal to claude (which under the
// native engine looks like squash-ide crashed — every spawned pane vanishes), it
// launches the interactive /log-task session as a regular pane that the
// responsive layout tabs/tiles like any /implement spawn (T-054). Unlike the
// superseded T-050 popover it is non-blocking: the add-task modal stays open and
// multiple sessions can run as tabs concurrently, so it deliberately does NOT set
// m.dispatching (that lock would block further adds).
//
// Each session gets a unique synthetic task id (log-task-N) so the auto-close
// watcher can address it via CloseByTask/DoneByTask — they are not real vault
// tasks. The same buildLogTaskPrompt / cmd.Dir / LookPath pre-flight as the tmux
// path is reused, so both engines build a byte-identical claude invocation.
//
// It mutates the receiver (the logTaskSeq counter, status) and returns the
// command that watches the tab for exit, plus a bool reporting whether the pane
// was spawned. A LookPath or Spawn failure is wrapped into logTaskErrMsg and
// reports ok=false — no vault was mutated (nothing to roll back, unlike the Enter
// spawn), so the caller keeps the form's contents for a retry.
func (m *Model) spawnLogTaskTab(f newTaskForm) (tea.Cmd, bool) {
	if _, err := lookPath("claude"); err != nil {
		return func() tea.Msg {
			return logTaskErrMsg{err: fmt.Errorf("claude CLI not found on PATH — install it to create tasks")}
		}, false
	}

	prompt := buildLogTaskPrompt(f)
	cmd := exec.Command("claude", prompt)
	// vault.ExpandHome resolves a leading `~` — the OS's chdir doesn't.
	cmd.Dir = vault.ExpandHome(m.vaultPath)

	m.logTaskSeq++
	id := fmt.Sprintf("log-task-%d", m.logTaskSeq)
	if _, err := m.manager.Spawn(pane.SpawnSpec{
		Command: cmd,
		TaskID:  id,
		Title:   strings.TrimSpace(f.name),
		Project: strings.TrimSpace(f.repo),
	}); err != nil {
		return func() tea.Msg {
			return logTaskErrMsg{err: fmt.Errorf("/log-task: %w", err)}
		}, false
	}

	m.statusMsg = "/log-task running in a new tab"
	m.statusIsErr = false
	uidebugf("log-task tab spawned %s", id)
	// Watch only this tab for exit (→ logTaskTabDoneMsg). The single self-arming
	// waitForPaneOutput loop started at init already repaints as claude draws, so
	// concurrent tabs don't each add a competing listener on the coalescing
	// repaint channel.
	return m.waitForLogTaskExit(id), true
}

// waitForLogTaskExit blocks on the /log-task tab's child-exit channel and emits
// logTaskTabDoneMsg when claude exits, so the tab can auto-close (T-054). Mirrors
// the deleted waitForModalExit, generalised to a task id. An unknown id yields a
// nil channel → a nil (no-op) command that never parks a goroutine or emits a
// spurious close.
func (m Model) waitForLogTaskExit(taskID string) tea.Cmd {
	done := m.manager.DoneByTask(taskID)
	if done == nil {
		return nil
	}
	return func() tea.Msg {
		<-done
		return logTaskTabDoneMsg{taskID: taskID}
	}
}

// buildLogTaskPrompt assembles the free-form $ARGUMENTS string that
// /log-task parses. Passed as a single argv entry, so no shell quoting is
// needed — newlines and punctuation are preserved verbatim.
func buildLogTaskPrompt(f newTaskForm) string {
	var b strings.Builder
	b.WriteString("/log-task ")
	b.WriteString(strings.TrimSpace(f.name))
	b.WriteString("\n\n")
	if p := strings.TrimSpace(f.repo); p != "" {
		b.WriteString("Project: ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("Type hint: ")
	b.WriteString(f.taskType())
	b.WriteString("\n")
	if body := strings.TrimSpace(f.prompt); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport = viewport.New(msg.Width, msg.Height-4)
		if m.view == detailView {
			m.updateDetailContent()
		}
		// Native mode reflows the in-process pane region; it never shells out
		// to tmux for sizing. Manager.Resize swallows a layout reject (keeps
		// the last good geometry), so this is crash-safe on shrink.
		if m.engineNative {
			m.manager.Resize(m.rightRegion())
			// The add-task form is a pure overlay render (T-054) recomputed from
			// m.width/m.height every View, so a resize needs no extra work here —
			// unlike the superseded T-050 popover, which owned a PTY child to resize.
			return m, nil
		}
		// Synchronous "too narrow" check — zoom/unzoom the TUI pane to
		// show a full-screen overlay when the terminal can't fit all panes.
		// Compact-mode check piggybacks on the same tmux call path.
		if tmux.InSession() {
			m.checkTooNarrow()
			m.checkCompactPane(tmux.CurrentPaneID())
		}
		return m, nil

	case tasksLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.allTasks = msg.tasks
		m.buildItems()
		m.applyFilter()
		if m.resetCursorOnLoad {
			m.cursor = 0
			m.resetCursorOnLoad = false
		}
		m.clampCursor()
		// Active count may have changed (dispatch / complete / deactivate /
		// block all route through loadTasks) — re-evaluate compact mode.
		// Native mode owns its own layout and never touches tmux here.
		if !m.engineNative && tmux.InSession() {
			m.checkCompactPane(tmux.CurrentPaneID())
		}
		// On the first load, respawn panes for active tasks left over from a
		// prior session. Done here rather than before the TUI starts so the
		// session is fully attached and sized. The two engines diverge: tmux
		// shells out via the RespawnFunc callback (set only on the tmux path in
		// main.go, also creating the placeholder); native spawns into the
		// in-process manager, which is owned by this tea.Program and so can only
		// be driven from the UI goroutine (T-049).
		if m.needsRespawn {
			if m.engineNative {
				m.needsRespawn = false
				m.respawnActivePanes()
			} else if m.RespawnFunc != nil {
				m.needsRespawn = false
				m.RespawnFunc(m.allTasks)
			}
		}
		// Kick the lifecycle progress poll once, now that active tasks are
		// loaded (T-053). pollProgressNow gives an immediate read; its handler
		// arms the recurring throttled tickProgress.
		if m.cfg.Progress.Show && !m.progressStarted {
			m.progressStarted = true
			return m, tea.Batch(tea.ClearScreen, m.scheduleProgress(0))
		}
		return m, tea.ClearScreen

	case dispatchDoneMsg:
		m.dispatching = false
		// Native engine: the worktree + vault are prepared; launch the pane into
		// the in-process manager now (the manager lives here, not in the dispatch
		// goroutine). Keep list focus — the new pane must not steal it ([[T-031]]).
		if m.engineNative && msg.native != nil {
			spec := pane.SpawnSpec{
				Command: msg.native.Command,
				TaskID:  msg.native.TaskID,
				Title:   msg.native.Title,
				Project: msg.native.Project,
			}
			if _, err := m.manager.Spawn(spec); err != nil {
				// PTY/layout failure after the vault was mutated — roll back so no
				// orphan active task or worktree is left behind.
				uidebugf("native spawn failed for %s: %v", msg.taskID, err)
				m.statusMsg = fmt.Sprintf("spawn failed for %s — rolled back: %v", msg.taskID, err)
				m.statusIsErr = true
				m.resetCursorOnLoad = true
				return m, m.runRollback(msg.task)
			}
		}
		m.statusMsg = fmt.Sprintf("spawned %s", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case completeDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("completed %s", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case blockDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("blocked %s", msg.taskID)
		m.statusIsErr = false
		return m, m.loadTasks

	case deactivateDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("deactivated %s → backlog", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case dispatchErrMsg:
		m.dispatching = false
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		// Reload tasks — the dispatch may have partially succeeded (e.g.
		// task moved to active before the spawn failed), so the vault
		// state may have changed. This also lets the "too narrow" overlay
		// trigger with the correct active-task count.
		return m, m.loadTasks

	case logTaskDoneMsg:
		// tmux / --no-tmux only: the fullscreen tea.ExecProcess /log-task child
		// exited. (Native uses the per-tab logTaskTabDoneMsg below instead.)
		m.dispatching = false
		m.statusMsg = "/log-task finished"
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case logTaskTabDoneMsg:
		// Native (T-054): a /log-task tab's claude child exited — auto-close that
		// pane (unlike the remain-on-exit /implement panes, [[T-035]]) and reload so
		// the freshly-filed task appears in the list. CloseByTask is idempotent
		// (unknown/closed id → clean no-op), so a force-closed or already-gone tab
		// is safe. Deliberately leaves creatingTask alone: the add-task modal's
		// lifecycle is independent of any one tab, and dispatching was never set.
		if m.engineNative {
			_ = m.manager.CloseByTask(msg.taskID)
		}
		m.statusMsg = "/log-task tab finished"
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		uidebugf("log-task tab %s finished", msg.taskID)
		return m, m.loadTasks

	case logTaskErrMsg:
		// Reused by both engines: the tmux runLogTask pre-flight/exec error and the
		// native spawnLogTaskTab LookPath/Spawn failure. The native add-task modal
		// is left open (creatingTask untouched) so the user can retry; the tmux path
		// already cleared the form at submit. No vault was mutated — nothing to roll
		// back. dispatching is cleared for the tmux path (native never set it).
		m.dispatching = false
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		return m, nil

	case paneOutputMsg:
		// A pane produced output / changed state; returning the model triggers
		// a re-View. Re-arm the listener for the next signal.
		return m, m.waitForPaneOutput()

	case statusTickMsg:
		old := m.subStatuses
		m.subStatuses = msg.statuses

		// Update tmux pane-border-format for any task whose state changed.
		//
		// "No entry" on this tick is treated as IDLE — matching activeBadge's
		// default for a nil sub — so the two consumers agree on what "no
		// live status report" means. Without the old-present / new-absent
		// branch, the pane border would silently retain its last-painted
		// format past the staleness horizon and diverge from the list badge.
		//
		// Native mode drives the pane border badges directly from the same
		// status states (no tmux), and honours any pending notify-click focus
		// request. The state diff mirrors the tmux arm: update from a present
		// entry; synthesise idle when an entry that was present goes absent/stale
		// (the [[T-023]] invariant); leave a never-seen task on its spawn-time
		// "working" badge rather than painting idle before claude's first report.
		if m.engineNative {
			// Advance the input_required badge-blink phase on every tick (T-040).
			m.manager.Tick()
			for _, t := range m.allTasks {
				if t.Status != "active" {
					continue
				}
				newSub, newOK := msg.statuses[t.ID]
				oldSub, oldOK := old[t.ID]
				switch {
				case newOK:
					// A stage-only entry (T-053) has State="" — no live activity
					// report. Paint it idle, not the default WORKING, so the pane
					// border agrees with the list badge (activeBadge does the same).
					state := newSub.State
					if state == "" {
						state = pane.StateIdle
					}
					m.manager.SetStateByTask(t.ID, state)
				case oldOK:
					m.manager.SetStateByTask(t.ID, pane.StateIdle)
				}
				// Re-arm focus-follows-input once the pane makes genuine progress
				// out of input_required (T-048). Only a *real* new report counts
				// (newOK): the synthesised idle from an absent status entry
				// (case oldOK above) is a transient gap, not progress, so a flap
				// must not resurrect the standing prompt the user walked away from.
				if newOK && oldOK && oldSub.State == pane.StateInputRequired && newSub.State != pane.StateInputRequired {
					if m.dismissedFocus[t.ID] {
						delete(m.dismissedFocus, t.ID)
						uidebugf("focus dismissal re-armed -> %s", t.ID)
					}
				}
				// Focus-follows-input (T-040): when a pane transitions into
				// input_required, surface it in the TUI — the in-TUI dual of the
				// [[T-034]] notification click. Gated on the transition (old state
				// != input_required) so focus isn't yanked every tick while the
				// pane waits. Suppressed when the user is mid-interaction (a modal
				// is open — about to spawn task B) or has deliberately dismissed
				// this pane with ctrl+w (T-048); the badge still updates via
				// SetStateByTask above so the pane visibly shows it needs input.
				if m.cfg.FocusFollowsInput && newOK && newSub.State == pane.StateInputRequired {
					wasInput := oldOK && oldSub.State == pane.StateInputRequired
					if !wasInput {
						switch {
						case m.inModalState():
							uidebugf("focus-follows-input suppressed (modal) -> %s", t.ID)
						case m.dismissedFocus[t.ID]:
							uidebugf("focus-follows-input suppressed (dismissed) -> %s", t.ID)
						default:
							if err := m.manager.FocusByTask(t.ID); err == nil {
								m.paneFocused = true
								uidebugf("focus-follows-input -> %s", t.ID)
							}
						}
					}
				}
			}
			if msg.focusTaskID != "" {
				if err := m.manager.FocusByTask(msg.focusTaskID); err == nil {
					m.paneFocused = true
					uidebugf("notify-click focus -> %s", msg.focusTaskID)
				}
			}
			return m, m.tickStatus()
		}

		// Native mode skips the tmux border sync entirely — pane badges are
		// painted by the manager from the same status states.
		if !m.engineNative && tmux.InSession() {
			tuiPane := tmux.CurrentPaneID()
			for _, t := range m.allTasks {
				if t.Status != "active" {
					continue
				}
				newSub, newOK := msg.statuses[t.ID]
				oldSub, oldOK := old[t.ID]
				// A stage-only entry (T-053, State="") is treated as idle — the
				// same collapse activeBadge applies — so the pane border never
				// flashes WORKING for a task with a stage but no live activity.
				newState := "idle"
				if newOK && newSub.State != "" {
					newState = newSub.State
				}
				oldState := "idle"
				if oldOK && oldSub.State != "" {
					oldState = oldSub.State
				}
				// Skip when we've never seen an entry for this task (both
				// old and new absent) — nothing has changed visually and we
				// don't want to paint "idle" on every tick forever.
				if !newOK && !oldOK {
					continue
				}
				if newState == oldState {
					continue
				}
				if pane, err := tmux.FindPaneByTask(tuiPane, t.ID); err == nil && pane != "" {
					_ = tmux.SetPaneBorderFormat(pane,
						spawner.TaskBorderFormatWithState(t.ID, t.Title, t.Project, newState))
				}
			}
		}
		return m, m.tickStatus()

	case progressTickMsg:
		// Cache the gh-detected pr/ci state and re-arm the throttled poll
		// (T-053). Whole-map replace: stale entries for completed tasks drop
		// out naturally as the active set shrinks.
		m.prProgress = msg.progress
		return m, m.scheduleProgress(progressPollInterval)

	case resourceTickMsg:
		// Fan the sampled usage out to the per-pane header readout (T-055), then
		// re-arm the 15s tick. Returning the model re-Views, so no requestRepaint
		// is needed (that path is for the async pane-output channel). A pid that
		// couldn't be sampled (OK=false) is logged at debug level and its pane is
		// told ok=false so the header drops the stats rather than showing stale
		// numbers; an exited pane freezes its last reading inside SetStats.
		for taskID, u := range msg.usage {
			if !u.OK {
				uidebugf("pane stats: sample failed for %s", taskID)
			}
			m.manager.SetStatsByTask(taskID, u.CPUPercent, u.CPUValid, u.RSSBytes, u.OK)
		}
		return m, m.tickResources()

	case tea.KeyMsg:
		newModel, cmd := m.handleKey(msg)
		// A keypress may have opened or closed a modal dialog, which
		// changes isCompact()'s truth value — re-check so the pane
		// expands back to normal while dialogs render and re-shrinks
		// once they close.
		if updated, ok := newModel.(Model); ok {
			if !updated.engineNative && tmux.InSession() {
				updated.checkCompactPane(tmux.CurrentPaneID())
				updated.checkFormZoom()
			}
			return updated, cmd
		}
		return newModel, cmd

	case tea.MouseMsg:
		// Native engine only — the tmux program is built without mouse support, so
		// it never receives a MouseMsg, but guard defensively. Click-to-focus and
		// forward-to-child (T-051); tmux handles its own mouse natively.
		if m.engineNative {
			return m.handleMouse(msg)
		}
		return m, nil
	}
	return m, nil
}

// handleMouse routes a mouse event under the native engine: a left-click (or any
// button press / wheel) on an unfocused pane *selects* it — the mouse dual of the
// ctrl+w focus-acquire gesture — and a press on the already-focused pane is
// *forwarded* to its child PTY via the tested EncodeMouse primitive, the
// symmetric mirror of handlePaneFocusedKey's gesture-vs-forward split. It is a
// no-op while a modal/dialog/detail/too-narrow view owns the screen (the pane
// region isn't drawn) or for a click that hits no addressable pane.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// A modal / dialog / detail view owns the screen and the pane region isn't
	// drawn — ignore the click so a stray press behind an overlay can't move focus.
	// Mirrors the focus-follows-input modal suppression (inModalState, T-048).
	if m.inModalState() {
		return m, nil
	}
	// Only presses (button clicks and wheel) route; plain motion and release are
	// ignored so a moving pointer never thrashes focus.
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	id, ok := m.manager.TaskAtPoint(msg.X, msg.Y)
	if !ok {
		// The click landed on the list, the gutter, or a tab strip — list-row and
		// tab-strip click-to-select are out of scope (T-051); leave focus as-is.
		return m, nil
	}
	// Focus-acquire: a click on a pane that is not the focused one (or while focus
	// is on the list) is the explicit "I want this pane" gesture — the mouse dual
	// of ctrl+w. The press is consumed, not forwarded. Clearing the pane's
	// dismissedFocus entry re-arms focus-follows-input for it: a deliberate user
	// gesture overrides the standing-prompt suppression (T-048 symmetry; delete on
	// a nil map is a safe no-op). FocusByTask's ErrUnknownPane (pane closed between
	// render and click) is swallowed so a stray click never flips the TUI into an
	// error state.
	if !m.paneFocused || id != m.manager.FocusedTaskID() {
		if err := m.manager.FocusByTask(id); err == nil {
			m.paneFocused = true
			delete(m.dismissedFocus, id)
			uidebugf("mouse focus -> %s", id)
		}
		return m, nil
	}
	// Forward-to-child: the press targets the already-focused pane, so deliver it
	// to the child PTY (in-pane clicks and wheel scroll reach claude's UI). An
	// unmappable event (EncodeMouse nil) is dropped; the write count/err is
	// discarded, matching handlePaneFocusedKey's idiom.
	if b := pane.EncodeMouse(msg); b != nil && m.manager != nil {
		_, _ = m.manager.WriteToFocused(b)
	}
	return m, nil
}

// inModalState reports whether a modal/dialog/form/filter/detail view currently
// owns the keyboard — the exact precedence set handleKey checks before routing a
// keystroke to the pane or list. Focus-follows-input (T-048) consults it so a
// background pane pausing for input never yanks focus out from under the user
// mid-interaction (e.g. while filling in the new-task form to spawn task B). Keep
// this in sync with the dispatch order in handleKey below.
func (m Model) inModalState() bool {
	return m.creatingTask != nil ||
		m.blocking != nil ||
		m.completing != nil ||
		m.deactivating != nil ||
		m.confirming != nil ||
		m.filterActive ||
		m.view == detailView
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// New-task form — takes precedence so keys (including the letters used
	// elsewhere for list actions) don't leak into the list while the form
	// is open.
	if m.creatingTask != nil {
		return m.handleNewTaskKey(msg)
	}

	// Block reason input
	if m.blocking != nil {
		return m.handleBlockInputKey(msg)
	}

	// Complete confirmation dialog
	if m.completing != nil {
		return m.handleCompleteConfirmKey(msg)
	}

	// Deactivate confirmation dialog
	if m.deactivating != nil {
		return m.handleDeactivateConfirmKey(msg)
	}

	// Spawn confirmation dialog
	if m.confirming != nil {
		return m.handleConfirmKey(msg)
	}

	// Filter input mode
	if m.filterActive {
		return m.handleFilterKey(msg)
	}

	// Detail view
	if m.view == detailView {
		return m.handleDetailKey(msg)
	}

	// Native pane focus: when the pane region owns focus, keystrokes route to
	// the focused child instead of the list. Reached only after the modal /
	// form / filter / detail checks above, so a dialog always wins.
	if m.engineNative && m.paneFocused {
		return m.handlePaneFocusedKey(msg)
	}

	// List view
	return m.handleListKey(msg)
}

// handlePaneFocusedKey routes a keystroke to the focused pane's child while the
// pane region owns focus. The focus-toggle key returns focus to the list and is
// never forwarded; ctrl+c always quits so the user is never trapped typing into
// a pane. Everything else is re-encoded (pane.EncodeKey) and written to the
// child's PTY — unmapped keys (function keys, etc.) are dropped, matching the
// encoder's documented gaps.
func (m Model) handlePaneFocusedKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.PaneFocus) {
		// Record the pane the user is deliberately leaving so focus-follows-input
		// stops re-surfacing its standing prompt (T-048). Cleared once the pane
		// makes genuine progress out of input_required (see statusTickMsg). A nil
		// map (tmux build) or no focused pane both no-op safely.
		if id := m.manager.FocusedTaskID(); id != "" {
			if m.dismissedFocus == nil {
				m.dismissedFocus = map[string]bool{}
			}
			m.dismissedFocus[id] = true
			uidebugf("focus dismissed -> %s", id)
		}
		m.paneFocused = false
		uidebugf("focus -> list")
		return m, nil
	}
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if b := pane.EncodeKey(msg); b != nil && m.manager != nil {
		_, _ = m.manager.WriteToFocused(b)
	}
	return m, nil
}

// closeNativePane tears down the native pane running taskID, if any. It is the
// Model-side half of complete/deactivate under engine=native — dispatch can't
// reach the in-process manager, so the lifecycle keypress closes the pane and
// dispatch only does the vault/worktree teardown. A no-op in tmux mode and for a
// task with no live pane (ErrUnknownPane swallowed). Returns focus to the list
// so the user isn't left typing into a region whose pane just vanished.
func (m *Model) closeNativePane(taskID string) {
	if !m.engineNative || m.manager == nil {
		return
	}
	_ = m.manager.CloseByTask(taskID)
	m.paneFocused = false
}

// cycleLayout advances the native pane layout columns→stack→tabs→responsive→
// columns and swaps the manager's strategy to match. A no-op in tmux mode. The
// status bar reports the new layout so the change is visible even with no panes.
func (m *Model) cycleLayout() {
	if !m.engineNative || m.manager == nil {
		return
	}
	m.layoutName = pane.NextLayoutName(m.layoutName)
	m.manager.SetStrategy(pane.StrategyForName(m.layoutName))
	m.statusMsg = "layout: " + m.layoutName
	m.statusIsErr = false
	uidebugf("layout cycle -> %s", m.layoutName)
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.confirming
		m.confirming = nil
		// Native pre-flight: reject before touching the vault if the region can't
		// fit another pane — the analogue of dispatch's tmux width check, so a
		// too-narrow terminal never orphans an active task.
		if m.engineNative && !m.manager.CanSpawn() {
			uidebugf("native spawn rejected for %s: region full", t.ID)
			m.statusMsg = fmt.Sprintf("can't spawn %s — widen the terminal (no room for another pane)", t.ID)
			m.statusIsErr = true
			return m, nil
		}
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("spawning %s...", t.ID)
		m.statusIsErr = false
		return m, m.runDispatch(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.confirming = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleCompleteConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.completing
		m.completing = nil
		// Native: close the pane here (the manager lives in the Model; dispatch's
		// teardown is a no-op under engine=native). Best-effort — an unknown task
		// is a no-op, so a task whose pane already died completes cleanly.
		m.closeNativePane(t.ID)
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("completing %s...", t.ID)
		m.statusIsErr = false
		return m, m.runComplete(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.completing = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleDeactivateConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.deactivating
		m.deactivating = nil
		// Native: close the pane here (parity with the tmux Deactivate teardown,
		// which kills the pane before removing the worktree).
		m.closeNativePane(t.ID)
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("deactivating %s...", t.ID)
		m.statusIsErr = false
		return m, m.runDeactivate(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.deactivating = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleNewTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	form, submitted, cancelled := m.creatingTask.handleKey(msg)
	if cancelled {
		m.creatingTask = nil
		// The handler-tail re-check covers this path too, but call
		// explicitly to keep the lifecycle symmetric with submit and to
		// guard against future refactors that change the tail behaviour.
		if tmux.InSession() {
			m.checkFormZoom()
		}
		return m, nil
	}
	if submitted {
		if m.dispatching {
			m.statusMsg = "another operation is in progress"
			m.statusIsErr = true
			return m, nil
		}
		// Native engine (T-054): launch the /log-task session as a regular tab and
		// keep the add-task modal open with a cleared form, so the user can queue
		// more tasks while sessions run concurrently. No dispatching lock — the
		// whole point is concurrent adds. Supersedes the T-050 blocking popover.
		if m.engineNative {
			cmd, ok := m.spawnLogTaskTab(form)
			if ok {
				// Clear the form in place for the next entry; the modal stays open.
				// On a spawn failure (ok == false) the form keeps its contents so
				// the user can retry — see spawnLogTaskTab / logTaskErrMsg.
				*m.creatingTask = newNewTaskForm()
			}
			return m, cmd
		}
		// tmux / --no-tmux: hand off to /log-task via the fullscreen
		// tea.ExecProcess takeover. We clear the form state before running so the
		// TTY handover is clean — tea.ExecProcess tears down the alt screen for the
		// duration, and we want the list to be what renders underneath if anything
		// flickers.
		m.creatingTask = nil
		m.dispatching = true
		m.statusMsg = "running /log-task..."
		m.statusIsErr = false
		// Un-zoom before tea.ExecProcess hands the TTY to claude — the
		// handler-tail re-check doesn't run on this branch (the cmd
		// suspends bubbletea before Update returns to it).
		if tmux.InSession() {
			m.checkFormZoom()
		}
		return m, m.runLogTask(form)
	}
	m.creatingTask = &form
	return m, nil
}

func (m Model) handleBlockInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.blocking = nil
		m.blockReason = ""
		return m, nil
	case msg.Type == tea.KeyEnter:
		if strings.TrimSpace(m.blockReason) == "" {
			m.statusMsg = "block reason cannot be empty"
			m.statusIsErr = true
			return m, nil
		}
		t := *m.blocking
		reason := m.blockReason
		m.blocking = nil
		m.blockReason = ""
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("blocking %s...", t.ID)
		m.statusIsErr = false
		return m, m.runBlock(t, reason)
	case msg.Type == tea.KeyBackspace:
		if len(m.blockReason) > 0 {
			m.blockReason = m.blockReason[:len(m.blockReason)-1]
		}
		return m, nil
	case msg.Type == tea.KeyRunes:
		m.blockReason += string(msg.Runes)
		return m, nil
	case msg.Type == tea.KeySpace:
		m.blockReason += " "
		return m, nil
	}
	return m, nil
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.filterActive = false
		m.filter = ""
		m.applyFilter()
		m.clampCursor()
	case msg.Type == tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.applyFilter()
			m.clampCursor()
		}
	case msg.Type == tea.KeyEnter:
		m.filterActive = false
	case msg.Type == tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.applyFilter()
		m.clampCursor()
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back), key.Matches(msg, keys.Enter):
		m.view = listView
		return m, nil
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Clear status message on any keypress
	m.statusMsg = ""

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case m.engineNative && key.Matches(msg, keys.PaneFocus):
		// Hand focus to the native pane region. Subsequent keys forward to the
		// focused child until toggled back. (With zero panes the region is the
		// placeholder and writes are no-ops until T-039 wires spawning.)
		m.paneFocused = true
		uidebugf("focus -> pane")
		return m, nil

	case m.engineNative && key.Matches(msg, keys.CycleLayout):
		m.cycleLayout()
		return m, nil

	case m.engineNative && key.Matches(msg, keys.NextTab):
		m.manager.FocusNext()
		return m, nil

	case m.engineNative && key.Matches(msg, keys.PrevTab):
		m.manager.FocusPrev()
		return m, nil

	case m.engineNative && key.Matches(msg, keys.Collapse):
		m.manager.ToggleCollapseFocused()
		uidebugf("collapse toggled")
		return m, nil

	case key.Matches(msg, keys.Up):
		m.moveCursor(-1)

	case key.Matches(msg, keys.Down):
		m.moveCursor(1)

	case key.Matches(msg, keys.Enter):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status == "backlog" {
				if m.dispatching {
					m.statusMsg = "dispatch already in progress"
					m.statusIsErr = true
					return m, nil
				}
				t := item.task
				m.confirming = &t
			} else {
				m.statusMsg = fmt.Sprintf("%s is already %s", item.task.ID, item.task.Status)
				m.statusIsErr = false
			}
		}

	case key.Matches(msg, keys.Complete):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be completed",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.completing = &t
		}

	case key.Matches(msg, keys.Block):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be blocked",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.blocking = &t
			m.blockReason = ""
		}

	case key.Matches(msg, keys.Deactivate):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be deactivated",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.deactivating = &t
		}

	case key.Matches(msg, keys.Detail):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			m.view = detailView
			m.updateDetailContent()
		}

	case key.Matches(msg, keys.Filter):
		m.filterActive = true
		m.filter = ""

	case key.Matches(msg, keys.Refresh):
		return m, m.loadTasks

	case key.Matches(msg, keys.NewTask):
		if m.dispatching {
			m.statusMsg = "another operation is in progress"
			m.statusIsErr = true
			return m, nil
		}
		f := newNewTaskForm()
		m.creatingTask = &f
		return m, nil
	}
	return m, nil
}

// moveCursor moves the cursor by delta, skipping non-selectable rows
// (section headers and empty-section placeholders).
func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	next := m.cursor + delta
	for next >= 0 && next < len(m.filtered) && !m.filtered[next].selectable() {
		next += delta
	}
	if next >= 0 && next < len(m.filtered) {
		m.cursor = next
	}
}

func (m *Model) clampCursor() {
	if len(m.filtered) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// If cursor is on a header or placeholder, walk forward to the first
	// selectable row; if none exists ahead, walk backward.
	if !m.filtered[m.cursor].selectable() {
		m.moveCursor(1)
		if !m.filtered[m.cursor].selectable() {
			m.moveCursor(-1)
		}
	}
}

func (m *Model) selectedItem() *displayItem {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return &m.filtered[m.cursor]
	}
	return nil
}

// buildItems groups tasks by status into display items with headers.
// The order — active, backlog, blocked — surfaces the work in flight
// first, matching the card-layout mockup.
//
// The ACTIVE section is always emitted, even when empty, with a dimmed
// placeholder item so launching tasks feels like a first-class affordance
// on an empty board. Other sections remain hidden when they have no
// content, to keep the board tight.
func (m *Model) buildItems() {
	m.items = nil
	if len(m.allTasks) == 0 {
		// The empty-vault path in View() renders its own message; don't
		// emit the ACTIVE placeholder as if tasks were loaded.
		return
	}
	statusOrder := []string{"active", "backlog", "blocked"}
	grouped := map[string][]task.Task{}
	for _, t := range m.allTasks {
		grouped[t.Status] = append(grouped[t.Status], t)
	}
	for _, status := range statusOrder {
		tasks := grouped[status]
		if len(tasks) == 0 && status != "active" {
			continue
		}
		m.items = append(m.items, displayItem{isHeader: true, header: status})
		if len(tasks) == 0 {
			m.items = append(m.items, displayItem{
				isPlaceholder: true,
				placeholder:   "No active tasks — select a task and press enter to launch",
			})
			continue
		}
		for _, t := range tasks {
			m.items = append(m.items, displayItem{task: t})
		}
	}
}

// applyFilter filters items by the current filter string.
func (m *Model) applyFilter() {
	if m.filter == "" {
		m.filtered = m.items
		return
	}
	query := strings.ToLower(m.filter)
	m.filtered = nil
	var lastHeader *displayItem
	for i := range m.items {
		item := m.items[i]
		if item.isHeader {
			lastHeader = &m.items[i]
			continue
		}
		// Placeholders never match a filter — they're an empty-section
		// hint, not searchable content.
		if item.isPlaceholder {
			continue
		}
		match := strings.Contains(strings.ToLower(item.task.ID), query) ||
			strings.Contains(strings.ToLower(item.task.Title), query)
		if match {
			if lastHeader != nil {
				m.filtered = append(m.filtered, *lastHeader)
				lastHeader = nil
			}
			m.filtered = append(m.filtered, item)
		}
	}
}

func (m *Model) updateDetailContent() {
	item := m.selectedItem()
	if item == nil || !item.selectable() {
		return
	}
	t := item.task

	title := fmt.Sprintf("%s — %s", t.ID, t.Title)
	if t.Status == "active" {
		title = activeIndicatorStyle.Render(activeIndicator) + " " + title
	}
	header := detailTitleStyle.Render(title)

	meta := fmt.Sprintf("  Type: %s  Project: %s  Status: %s  Priority: %s",
		t.Type, t.Project, t.Status, t.Priority)

	var extra string
	if t.Status == "active" {
		if wt, err := dispatch.WorktreePathFor(m.cfg, t); err == nil {
			extra = "\n" + worktreeStyle.Render("Worktree: "+wt)
		}
	}

	// Full lifecycle-stage breakdown for the selected active task (T-053): a
	// row per stage, favouring vertical space over a cramped strip.
	var progress string
	if t.Status == "active" && m.cfg.Progress.Show {
		stage := ""
		if s, ok := m.subStatuses[t.ID]; ok {
			stage = s.Stage
		}
		progress = "\n\n" + m.renderStageBreakdown(t, stage, m.prProgress[t.ID])
	}

	body := detailBodyStyle.Render(t.Body)
	content := header + "\n" + meta + extra + progress + "\n\n" + body

	m.viewport.SetContent(content)
	m.viewport.GotoTop()
}

// renderStageBreakdown renders the detail-view lifecycle section: a "Progress:"
// heading, one labelled traffic-light row per stage, and a trailing activity
// line (the current state/message + how long ago it was reported). stage is the
// reported lifecycle stage (from the merged status file); prog the gh-detected
// pr/ci state.
func (m Model) renderStageBreakdown(t task.Task, stage string, prog ghProgress) string {
	lights := deriveStageLights(stage, prog)
	var b strings.Builder
	b.WriteString(detailBodyStyle.Render(sectionLabelStyle.Render("Progress")))
	b.WriteString("\n")
	for i, l := range lights {
		b.WriteString(detailBodyStyle.Render("  " + l.style().Render(l.glyph()) + " " + progressLabelStyle.Render(status.StageOrder[i])))
		b.WriteString("\n")
	}
	if s, ok := m.subStatuses[t.ID]; ok && s.State != "" {
		line := fmt.Sprintf("  %s", s.State)
		if s.Message != "" {
			line += " — " + s.Message
		}
		if s.Updated > 0 {
			line += fmt.Sprintf("  (%s ago)", relativeSince(s.Updated))
		}
		b.WriteString(detailBodyStyle.Render(progressLabelStyle.Render(line)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// relativeSince renders a unix timestamp as a coarse "Ns / Nm / Nh" age,
// matching the staleness-horizon granularity the status pipeline cares about.
func relativeSince(unix int64) string {
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// View renders the UI.
// checkTooNarrow queries the tmux window width and zooms/unzooms the TUI
// pane to show a "too narrow" overlay. Called synchronously from the
// WindowSizeMsg handler (~5ms tmux roundtrip, no async race).
func (m *Model) checkTooNarrow() {
	pane := tmux.CurrentPaneID()
	if pane == "" {
		return
	}
	ww, err := tmux.WindowWidth(pane)
	if err != nil || ww == 0 {
		return
	}
	activeCount := 0
	for _, t := range m.allTasks {
		if t.Status == "active" {
			activeCount++
		}
	}
	if activeCount == 0 {
		if m.tooNarrow {
			m.tooNarrow = false
			tmux.ToggleZoom(pane)
		}
		return
	}
	needed := m.cfg.Tmux.TUIWidth + activeCount*(m.cfg.Tmux.PaneWidth+1)
	if ww < needed && !m.tooNarrow {
		m.tooNarrow = true
		tmux.ToggleZoom(pane)
	} else if ww >= needed && m.tooNarrow {
		m.tooNarrow = false
		tmux.ToggleZoom(pane)
	}
}

// checkFormZoom mirrors checkTooNarrow's transition-only state machine for
// the new-task form: while the form is open we want the TUI pane zoomed so
// the form covers the spawned agent panes (or placeholder) and renders at
// full window width — the form is unusable when the pane is pinned to
// CompactListWidth=20.
//
// Coordinates with checkTooNarrow so the two consumers can never emit an
// unbalanced ToggleZoom: if checkTooNarrow already holds zoom on, we record
// formZoomed=false and emit no toggle, so closing the form won't release
// tooNarrow's overlay.
func (m *Model) checkFormZoom() {
	pane := tmux.CurrentPaneID()
	if pane == "" {
		return
	}
	if m.tooNarrow {
		m.formZoomed = false
		return
	}
	want := m.creatingTask != nil
	if want && !m.formZoomed {
		m.formZoomed = true
		tmux.ToggleZoom(pane)
	} else if !want && m.formZoomed {
		m.formZoomed = false
		tmux.ToggleZoom(pane)
	}
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.\n", m.err)
	}

	// Native engine composes the list + pane region itself (and its own
	// too-narrow overlay); it never uses the tmux tooNarrow/compact path.
	if m.engineNative {
		return m.nativeView()
	}

	if m.tooNarrow {
		activeCount := 0
		for _, t := range m.allTasks {
			if t.Status == "active" {
				activeCount++
			}
		}
		needed := m.cfg.Tmux.TUIWidth + activeCount*(m.cfg.Tmux.PaneWidth+1)
		msg := fmt.Sprintf(
			"Terminal too narrow\n\nNeeded: %d cols\n\nWiden the terminal or\ndeactivate a task with d",
			needed,
		)
		styled := lipgloss.NewStyle().
			Foreground(lipgloss.Color("204")).
			Bold(true).
			Align(lipgloss.Center).
			Render(msg)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, styled)
	}

	if m.view == detailView {
		return m.detailViewRender()
	}
	return m.listViewRender()
}

func (m Model) listViewRender() string {
	// The task list width comes from one of three paths. The tmux path
	// (isCompact — narrow terminal + 2+ active spawns) pins the pane to
	// CompactListWidth. The native path scales the list responsively between
	// CompactListWidth and tuiWidth, ceding the rest to the pane region (see
	// nativeListWidth). Everything else clamps the terminal width to
	// [40, maxWidth].
	maxWidth := m.cfg.Tmux.TUIWidth
	if maxWidth <= 0 {
		maxWidth = 60
	}
	var width int
	switch {
	case m.isCompact():
		width = CompactListWidth
	case m.engineNative:
		width = m.nativeListWidth()
	default:
		width = m.width
		if width > maxWidth {
			width = maxWidth
		}
		if width < 40 {
			width = 40
		}
	}

	// Compact chrome (condensed top bar, denser cards, short help) engages only
	// once the list is too narrow for the full layout's expanded cards to stay
	// legible (fullChromeMinWidth). The width above scales continuously; the
	// chrome switches at this step, so full chrome holds across most of the
	// list's range and only collapses near the compact floor.
	compact := width < fullChromeMinWidth

	// Clamp every line to the list width. The help/status lines are wider than
	// the list (the full help is ~117 cols), and unlike the tmux engine — where
	// the pane boundary clips the column for free — the native engine joins this
	// block directly against the pane region. Without the clip the widest line
	// would dictate the column width, shoving the panes off-screen. MaxWidth
	// truncates the overflow, restoring the parity tmux got from its pane edge.
	clamp := func(s string) string {
		return lipgloss.NewStyle().MaxWidth(width).Render(s)
	}

	var b strings.Builder

	// Top bar: app name + per-status counts. Compact variant uses a
	// single-char stub + two-char counts to fit CompactListWidth.
	counts := m.statusCounts()
	if compact {
		b.WriteString(renderTopBarCompact(width, counts))
	} else {
		b.WriteString(renderTopBar(width, "squash-ide", "", counts))
	}
	b.WriteString("\n")

	// New-task form takes over the whole pane — there's usually a lot of
	// information to enter (particularly the prompt), so the bottom-overlay
	// pattern used for simple y/N dialogs isn't enough. Native engine only floats
	// the form as a centered overlay (nativeView composites it over the list +
	// panes, T-054), so the fullscreen takeover is gated to the tmux/--no-tmux
	// path — under native this block must not replace the list.
	if !m.engineNative && m.creatingTask != nil {
		formHeight := m.height - 2 // leave room for the status bar + help
		if formHeight < 16 {
			formHeight = 16
		}
		b.Reset()
		b.WriteString(m.creatingTask.view(width, formHeight))
		b.WriteString("\n")
		b.WriteString(m.renderStatusBar())
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("[tab] field  [←/→] type  [enter] submit  [ctrl+d] submit from prompt  [esc] cancel"))
		return clamp(b.String())
	}

	if len(m.allTasks) == 0 {
		b.WriteString(emptyStyle.Render("No tasks found in vault."))
		b.WriteString("\n")
		b.WriteString(emptyStyle.Render(fmt.Sprintf("Vault: %s", m.vaultPath)))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("[r] refresh  [q] quit"))
		b.WriteString("\n")
		return clamp(b.String())
	}

	// Reserve rows: top bar + divider + (filter row?) + status bar + help.
	chrome := 3 // top bar + status bar + help line
	if m.filterActive || m.filter != "" {
		chrome++
	}
	if m.confirming != nil || m.completing != nil || m.deactivating != nil || m.blocking != nil {
		chrome += 3 // dialog box height (border + content + border)
	}
	listHeight := m.height - chrome
	if listHeight < 5 {
		listHeight = 5
	}

	b.WriteString(m.renderCardList(width, listHeight, compact))

	// Dialog overlays.
	if m.confirming != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Spawn %s? [y/N]", m.confirming.ID)))
		b.WriteString("\n")
	} else if m.completing != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Complete %s? [y/N]", m.completing.ID)))
		b.WriteString("\n")
	} else if m.deactivating != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Deactivate %s → backlog? [y/N]", m.deactivating.ID)))
		b.WriteString("\n")
	} else if m.blocking != nil {
		prompt := fmt.Sprintf("Block %s — reason: %s█", m.blocking.ID, m.blockReason)
		b.WriteString(inputBoxStyle.Render(prompt))
		b.WriteString("\n")
	}

	// Filter bar
	if m.filterActive {
		b.WriteString(filterPromptStyle.Render("/") + filterInputStyle.Render(m.filter+"█"))
		b.WriteString("\n")
	} else if m.filter != "" {
		b.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %s", m.filter)) + "  ")
		b.WriteString(helpStyle.Render("[/] edit  [esc] clear"))
		b.WriteString("\n")
	}

	// Status bar (transient messages only — counts moved to top bar).
	b.WriteString(m.renderStatusBar())
	b.WriteString("\n")

	// Help
	switch {
	case m.confirming != nil, m.completing != nil, m.deactivating != nil:
		b.WriteString(helpStyle.Render("[y/enter] confirm  [n/esc] cancel"))
	case m.blocking != nil:
		b.WriteString(helpStyle.Render("[enter] submit  [esc] cancel  [type] reason"))
	case compact:
		// Compact stands down in dialog/blocking states (see isCompact) so
		// the only reachable cases here are default and filter-active.
		b.WriteString(helpLineCompact(m.filterActive, m.filter != ""))
	case m.filterActive:
		b.WriteString(helpStyle.Render("[enter] apply  [esc] clear  [type] filter"))
	case m.engineNative && m.paneFocused:
		b.WriteString(helpStyle.Render("pane focus — keys go to the task  [ctrl+w] back to list  [ctrl+c] quit"))
	case m.engineNative:
		b.WriteString(helpStyle.Render("j/k nav  enter spawn  ctrl+w pane  L layout  [/] tabs  z collapse  t new  c done  d deact  b block  / filter  q quit"))
	default:
		b.WriteString(helpStyle.Render("j/k nav  enter spawn  t new  c complete  d deactivate  b block  tab detail  / filter  r refresh  q quit"))
	}
	b.WriteString("\n")

	return clamp(b.String())
}

// renderCardList renders the per-section card list, scrolling to keep the
// cursor visible. Cards have variable height (active = 3 lines, backlog = 2;
// in compact mode: active = 2, backlog = 1) so we render to a flat line
// buffer first, find the cursor card's line range, then slice.
func (m Model) renderCardList(width, height int, compact bool) string {
	var (
		lines       []string
		cursorStart = -1
		cursorEnd   = -1
	)

	for i, item := range m.filtered {
		if item.isHeader {
			// Section header gets a leading blank for breathing room
			// (skipped at the very top of the list).
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, renderSectionHeader(item.header))
			lines = append(lines, "")
			continue
		}

		if item.isPlaceholder {
			lines = append(lines, renderPlaceholder(item.placeholder))
			lines = append(lines, "")
			continue
		}

		selected := i == m.cursor
		var sub *status.File
		if s, ok := m.subStatuses[item.task.ID]; ok {
			sub = &s
		}
		prog := m.prProgress[item.task.ID]
		showStrip := m.cfg.Progress.Show
		card := renderCard(item.task, selected, width, sub, compact, prog, showStrip)

		if selected {
			cursorStart = len(lines)
			cursorEnd = len(lines) + len(card) - 1
		}
		lines = append(lines, card...)
		// Spacer between cards.
		lines = append(lines, "")
	}

	// Trim trailing blank.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Scroll: keep cursor card fully visible.
	start := 0
	if cursorEnd >= 0 && len(lines) > height {
		switch {
		case cursorEnd-cursorStart+1 > height:
			start = cursorStart
		case cursorEnd >= height:
			start = cursorEnd - height + 1
		}
		if start < 0 {
			start = 0
		}
		if start > len(lines)-height {
			start = len(lines) - height
		}
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	// Pad to exactly `height` lines so the card list always fills its
	// allocated space, pinning the footer (status bar, help) to the bottom.
	result := make([]string, height)
	copy(result, lines[start:end])
	return strings.Join(result, "\n") + "\n"
}

// statusCounts returns a {status: count} map across all loaded tasks.
func (m Model) statusCounts() map[string]int {
	counts := map[string]int{}
	for _, t := range m.allTasks {
		counts[t.Status]++
	}
	return counts
}

func (m Model) detailViewRender() string {
	header := helpStyle.Render("[enter/esc] back  [↑↓] scroll  [q] quit")
	return m.viewport.View() + "\n" + header + "\n"
}

// renderStatusBar shows transient feedback (success / error / dispatching)
// or a quiet vault hint when idle. Per-status counts now live in the top
// bar, so the bottom bar stays free for the message of the moment.
func (m Model) renderStatusBar() string {
	if m.statusMsg != "" {
		if m.dispatching {
			return dispatchingStyle.Render(m.statusMsg)
		}
		if m.statusIsErr {
			return statusErrorStyle.Render(m.statusMsg)
		}
		return statusSuccessStyle.Render(m.statusMsg)
	}
	return statusBarStyle.Render(fmt.Sprintf("Vault: %s", m.vaultPath))
}
