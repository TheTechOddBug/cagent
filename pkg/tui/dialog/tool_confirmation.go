package dialog

import (
	"github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/tui/animation"
	"github.com/docker/docker-agent/pkg/tui/dialog/toolconfirmation"
	"github.com/docker/docker-agent/pkg/tui/service"
)

type (
	RuntimeResumeMsg         = toolconfirmation.RuntimeResumeMsg
	ToolConfirmationResponse = toolconfirmation.ToolConfirmationResponse
	ConfirmationSessionState = toolconfirmation.ConfirmationSessionState
)

var (
	_ ConfirmationSessionState = (*service.SessionState)(nil)
	_ ConfirmationSessionState = (*service.EmbeddedSessionState)(nil)
)

func NewToolConfirmationDialog(ar *animation.Runtime, msg *runtime.ToolCallConfirmationEvent, sessionState ConfirmationSessionState) Dialog {
	return toolconfirmation.NewToolConfirmationDialog(ar, msg, sessionState)
}
