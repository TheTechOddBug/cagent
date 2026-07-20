package sidebar

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tui/service"
)

// TestSidebar_HandleClickType_Usage_Vertical verifies that a click anywhere
// on the vertical Token Usage section (title included) reports ClickUsage.
func TestSidebar_HandleClickType_Usage_Vertical(t *testing.T) {
	t.Parallel()

	sess := session.New()
	sessionState := service.NewSessionState(sess)
	sb := New(t.Context(), sessionState)

	m := sb.(*model)
	m.sessionHasContent = true
	m.titleGenerated = true
	m.sessionTitle = "Test"
	m.width = 40
	m.height = 50

	// Force a render to record the usage click zone
	_ = sb.View()

	require.Less(t, m.usageZoneStart, m.usageZoneEnd, "the usage zone must be recorded")
	assert.Contains(t, ansi.Strip(m.cachedLines[m.usageZoneStart]), "Token Usage")

	paddingLeft := m.layoutCfg.PaddingLeft
	for y := m.usageZoneStart; y < m.usageZoneEnd; y++ {
		result, _ := sb.HandleClickType(paddingLeft+2, y)
		assert.Equalf(t, ClickUsage, result, "line %d of the usage section should report ClickUsage", y)
	}
}

// TestSidebar_HandleClickType_Usage_Vertical_Hidden verifies a hidden usage
// section leaves no stale click zone behind.
func TestSidebar_HandleClickType_Usage_Vertical_Hidden(t *testing.T) {
	t.Parallel()

	sess := session.New()
	sessionState := service.NewSessionState(sess)
	sb := New(t.Context(), sessionState)

	m := sb.(*model)
	m.width = 40
	m.height = 50
	m.SetSectionVisibility(SectionVisibility{HideUsage: true})

	_ = sb.View()

	paddingLeft := m.layoutCfg.PaddingLeft
	for y := range len(m.cachedLines) {
		result, _ := sb.HandleClickType(paddingLeft+2, y)
		assert.NotEqualf(t, ClickUsage, result, "hidden usage must not be clickable (line %d)", y)
	}
}

// TestSidebar_HandleClickType_Usage_Collapsed_SharedLine verifies the
// right-aligned usage reading on the shared path/usage row reports
// ClickUsage while the path part keeps reporting ClickWorkingDir.
func TestSidebar_HandleClickType_Usage_Collapsed_SharedLine(t *testing.T) {
	t.Parallel()

	sess := session.New()
	sessionState := service.NewSessionState(sess)
	sb := New(t.Context(), sessionState)

	m := sb.(*model)
	m.sessionHasContent = true
	m.titleGenerated = true
	m.mode = ModeCollapsed
	m.width = 80
	m.sessionTitle = "Hi"
	m.workingDirectory = "~/projects/myapp"

	vm := m.computeCollapsedViewModel(m.contentWidth(false))
	require.True(t, vm.WdAndUsageOnOneLine, "path and usage must share one line at this width")
	require.NotEmpty(t, vm.UsageSummary)

	paddingLeft := m.layoutCfg.PaddingLeft
	rowY := vm.titleSectionLines()
	usageX := vm.ContentWidth - lipgloss.Width(vm.UsageSummary)

	result, _ := sb.HandleClickType(paddingLeft+usageX+1, rowY)
	assert.Equal(t, ClickUsage, result, "click on the usage reading should report ClickUsage")

	result, _ = sb.HandleClickType(paddingLeft+3, rowY)
	assert.Equal(t, ClickWorkingDir, result, "the path part of the row stays a working dir target")
}

// TestSidebar_HandleClickType_Usage_Collapsed_OwnLine verifies the usage
// reading is clickable when it takes its own row (no session path).
func TestSidebar_HandleClickType_Usage_Collapsed_OwnLine(t *testing.T) {
	t.Parallel()

	sess := session.New()
	sessionState := service.NewSessionState(sess)
	sb := New(t.Context(), sessionState)

	m := sb.(*model)
	m.sessionHasContent = true
	m.titleGenerated = true
	m.mode = ModeCollapsed
	m.width = 50
	m.sessionTitle = "Hi"
	m.workingDirectory = ""

	vm := m.computeCollapsedViewModel(m.contentWidth(false))
	require.NotEmpty(t, vm.UsageSummary)

	paddingLeft := m.layoutCfg.PaddingLeft
	result, _ := sb.HandleClickType(paddingLeft+3, vm.titleSectionLines())
	assert.Equal(t, ClickUsage, result, "click on the usage row should report ClickUsage")
}

// TestSidebar_HandleClickType_Usage_Collapsed_Hidden verifies a hidden usage
// section keeps no hit target in the collapsed band.
func TestSidebar_HandleClickType_Usage_Collapsed_Hidden(t *testing.T) {
	t.Parallel()

	sess := session.New()
	sessionState := service.NewSessionState(sess)
	sb := New(t.Context(), sessionState)

	m := sb.(*model)
	m.mode = ModeCollapsed
	m.width = 50
	m.sessionTitle = "Hi"
	m.SetSectionVisibility(SectionVisibility{HideUsage: true})

	paddingLeft := m.layoutCfg.PaddingLeft
	for y := range 6 {
		result, _ := sb.HandleClickType(paddingLeft+3, y)
		assert.NotEqualf(t, ClickUsage, result, "hidden usage must not be clickable (line %d)", y)
	}
}
