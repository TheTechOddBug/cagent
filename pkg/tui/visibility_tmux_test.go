//go:build !windows

package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/service"
)

type visibilityTestState struct {
	Elapsed       time.Duration
	Probed        bool
	Hidden        bool
	Focus, Blur   int
	Subscriptions int32
}

type visibilityTestModel struct {
	root  *appModel
	state visibilityTestState
	path  string
	sub   animation.Subscription
	extra animation.Subscription
}

func (m *visibilityTestModel) Init() tea.Cmd {
	return tea.Batch(m.root.checkTmuxVisibilityCmd(), m.sub.Start())
}

func (m *visibilityTestModel) View() tea.View { return m.root.View() }

func (m *visibilityTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var extra tea.Cmd
	switch msg := msg.(type) {
	case tmuxVisibilityMsg:
		m.state.Probed, m.state.Hidden = true, msg.hidden
	case tea.FocusMsg:
		m.state.Focus++
	case tea.BlurMsg:
		m.state.Blur++
	case tea.KeyPressMsg:
		switch msg.Text {
		case "w":
			extra = m.extra.Start()
		case "q":
			return m, tea.Quit
		}
	}
	_, cmd := m.root.Update(msg)
	m.state.Hidden = m.root.paneHidden
	m.state.Elapsed = m.root.ar.Now()
	m.state.Subscriptions = m.root.ar.ActiveCount()
	data, err := json.Marshal(m.state)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(m.path+".tmp", data, 0o600); err != nil {
		panic(err)
	}
	if err := os.Rename(m.path+".tmp", m.path); err != nil {
		panic(err)
	}
	return m, tea.Batch(cmd, extra)
}

// The helper runs the production root, renderer and input parser inside a real
// tmux pane. Only its chat content and instrumentation are test-specific.
func TestTmuxVisibilityHelper(t *testing.T) {
	path := os.Getenv("DOCKER_AGENT_TEST_VISIBILITY_STATE")
	if path == "" {
		t.Skip("subprocess helper")
	}
	root, _, _ := wallClockRoot(t, 80, 24)
	defer root.ar.Stop()
	root.activeTab.chatPage = &countingChatPage{text: "focus integration", dirtyTick: true}
	root.activeTab.sessionState = &service.SessionState{}
	root.ready, root.leanMode, root.appName = true, true, "focus-test"
	model := &visibilityTestModel{root: root, path: path, sub: root.ar.Subscribe(), extra: root.ar.Subscribe()}
	_, err := tea.NewProgram(model, tea.WithContext(t.Context())).Run()
	require.NoError(t, err)
}

func TestTmuxVisibilityLifecycle(t *testing.T) {
	for _, focusEvents := range []string{"off", "on"} {
		t.Run("focus-events="+focusEvents, func(t *testing.T) {
			testTmuxVisibilityLifecycle(t, focusEvents)
		})
	}
}

func testTmuxVisibilityLifecycle(t *testing.T, focusEvents string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// macOS sun_path is shorter than a t.TempDir path containing this test name.
	dir, err := os.MkdirTemp("", "focus") //nolint:forbidigo,usetesting // private short Unix socket path
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "tmux.sock")
	base := []string{"-S", socket, "-f", "/dev/null"}
	tmux := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "tmux", append(base, args...)...).CombinedOutput()
		require.NoError(t, err, "tmux %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	tmux("new-session", "-d", "-s", "focus-test", "exec cat")
	t.Cleanup(func() {
		// Only ever target this test's private server, never the user's tmux.
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run() //nolint:noctx // cleanup must outlive the test context
	})
	tmux("set-option", "-s", "focus-events", focusEvents)
	statePath := filepath.Join(dir, "state")
	executable, err := os.Executable()
	require.NoError(t, err)
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	command := "exec env DOCKER_AGENT_TEST_VISIBILITY_STATE=" + quote(statePath) + " " + quote(executable) + " -test.run=^TestTmuxVisibilityHelper$ -test.timeout=90s"
	tmux("respawn-pane", "-k", "-t", "focus-test", command)

	readState := func() visibilityTestState {
		t.Helper()
		data, _ := os.ReadFile(statePath)
		var state visibilityTestState
		_ = json.Unmarshal(data, &state)
		return state
	}
	require.Eventually(t, func() bool { return readState().Probed }, 10*time.Second, 20*time.Millisecond, "startup snapshot was not applied")
	snapshot := tmux("display-message", "-p", "-t", "focus-test", tmuxVisibilityFormat)
	fields := strings.Fields(snapshot)
	if len(fields) != 4 || fields[0] != "0" || fields[1] != "0" || fields[2] != "1" {
		require.False(t, readState().Hidden, "unsupported formats must leave rendering enabled")
		before := readState().Elapsed
		require.Eventually(t, func() bool { return readState().Elapsed > before }, 5*time.Second, 20*time.Millisecond)
		t.Skipf("tmux lacks required visibility formats: %q; verified fail-open behavior", snapshot)
	}
	require.True(t, readState().Hidden, "detached startup must be detected even without an initial blur: %s", snapshot)
	assertStopped := func() {
		t.Helper()
		before := readState().Elapsed
		assert.Never(t, func() bool { return readState().Elapsed != before }, 400*time.Millisecond, 20*time.Millisecond, "hidden pane accepted animation ticks")
	}
	assertStopped()

	tmux("send-keys", "-t", "focus-test", "w")
	require.Eventually(t, func() bool { return readState().Subscriptions == 2 }, 5*time.Second, 20*time.Millisecond, "hidden root did not process new work")
	assertStopped()

	attach := exec.CommandContext(t.Context(), "tmux", append(base, "attach-session", "-t", "focus-test")...)
	attach.Env = append(os.Environ(), "TMUX=", "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(attach, &pty.Winsize{Rows: 24, Cols: 80})
	require.NoError(t, err)
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, terminal); close(drained) }()
	attachDone := make(chan error, 1)
	go func() { attachDone <- attach.Wait() }()
	t.Cleanup(func() {
		_ = terminal.Close()
		<-drained
		select {
		case <-attachDone:
		case <-time.After(3 * time.Second):
			_ = attach.Process.Kill()
			<-attachDone
		}
	})
	before := readState().Elapsed
	require.Eventually(t, func() bool { return !readState().Hidden && readState().Elapsed > before }, 5*time.Second, 20*time.Millisecond, "attach did not resume animations")
	// Switching to a split must keep the unfocused agent visible and animating.
	agentPane := tmux("display-message", "-p", "-t", "focus-test", "#{pane_id}")
	sibling := tmux("split-window", "-h", "-t", "focus-test", "-P", "-F", "#{pane_id}", "exec cat")
	before = readState().Elapsed
	require.Eventually(t, func() bool { return readState().Elapsed > before && !readState().Hidden }, 5*time.Second, 20*time.Millisecond)

	tmux("resize-pane", "-Z", "-t", sibling)
	require.Eventually(t, func() bool { return readState().Hidden }, 5*time.Second, 20*time.Millisecond, "zoomed sibling must hide the agent")
	assertStopped()
	tmux("resize-pane", "-Z", "-t", sibling)
	before = readState().Elapsed
	require.Eventually(t, func() bool { return readState().Elapsed > before && !readState().Hidden }, 5*time.Second, 20*time.Millisecond, "unzoom must resume an inactive split without focus events")

	otherWindow := tmux("new-window", "-t", "focus-test", "-P", "-F", "#{window_id}", "exec cat")
	require.Eventually(t, func() bool { return readState().Hidden }, 5*time.Second, 20*time.Millisecond, "background window must hide the agent")
	assertStopped()

	// Choosers can preview the agent without making its window current.
	previewPane := tmux("display-message", "-p", "-t", otherWindow, "#{pane_id}")
	tmux("choose-tree", "-w", "-t", previewPane)
	tmux("send-keys", "-t", previewPane, "Up")
	before = readState().Elapsed
	require.Eventually(t, func() bool { return !readState().Hidden && readState().Elapsed > before }, 5*time.Second, 20*time.Millisecond, "tree previews must keep background panes live")

	// A preview in an unfocused split remains visible too.
	tmux("split-window", "-h", "-t", previewPane, "exec cat")
	require.Equal(t, "0", tmux("display-message", "-p", "-t", previewPane, "#{pane_active}"))
	before = readState().Elapsed
	require.Never(t, func() bool { return readState().Hidden }, 2*tmuxVisibilityInterval, 20*time.Millisecond, "inactive split previews must stay live")
	require.Greater(t, readState().Elapsed, before)
	tmux("send-keys", "-t", previewPane, "q")
	require.Eventually(t, func() bool { return readState().Hidden }, 5*time.Second, 20*time.Millisecond, "closing the preview must restore hidden-pane savings")
	assertStopped()

	// The window is still visible when linked into another client's session.
	tmux("new-session", "-d", "-s", "linked", "exec cat")
	tmux("link-window", "-s", agentPane, "-t", "linked:1")
	tmux("select-window", "-t", "linked:1")
	tmux("switch-client", "-t", "linked")
	before = readState().Elapsed
	require.Eventually(t, func() bool { return readState().Elapsed > before && !readState().Hidden }, 5*time.Second, 20*time.Millisecond, "linked visible window must resume")

	tmux("switch-client", "-t", "focus-test")
	tmux("select-window", "-t", otherWindow)
	require.Eventually(t, func() bool { return readState().Hidden }, 5*time.Second, 20*time.Millisecond)
	tmux("select-window", "-t", agentPane)
	require.Eventually(t, func() bool { return !readState().Hidden }, 5*time.Second, 20*time.Millisecond)

	tmux("detach-client", "-s", "focus-test")
	require.Eventually(t, func() bool { return readState().Hidden }, 5*time.Second, 20*time.Millisecond, "detach must be detected without focus events")
	assertStopped()

	// Control frontends consume pane output even from noncurrent windows.
	control := exec.CommandContext(t.Context(), "tmux", append(base, "-C", "attach-session", "-t", "focus-test")...)
	control.Env = append(os.Environ(), "TMUX=")
	input, err := control.StdinPipe()
	require.NoError(t, err)
	control.Stdout, control.Stderr = io.Discard, io.Discard
	require.NoError(t, control.Start())
	t.Cleanup(func() {
		_ = input.Close()
		_ = control.Process.Kill()
		_ = control.Wait()
	})
	tmux("select-window", "-t", otherWindow)
	before = readState().Elapsed
	require.Eventually(t, func() bool { return !readState().Hidden && readState().Elapsed > before }, 5*time.Second, 20*time.Millisecond, "control clients may display background windows")
	tmux("select-window", "-t", agentPane)
	tmux("resize-pane", "-Z", "-t", sibling)
	before = readState().Elapsed
	require.Never(t, func() bool { return readState().Hidden }, 2*tmuxVisibilityInterval, 20*time.Millisecond, "control clients may display zoomed siblings")
	require.Greater(t, readState().Elapsed, before)
	t.Logf("tmux lifecycle: %s", fmt.Sprint(readState()))
	tmux("send-keys", "-t", agentPane, "q")
}
