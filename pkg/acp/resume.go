package acp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/coder/acp-go-sdk"
)

func validateResumeWorkingDir(saved, requested string) error {
	if saved == "" {
		if requested != "" {
			return errors.New("session has no saved working directory; create a new session to select a workspace")
		}
		return nil
	}
	savedInfo, err := os.Stat(saved)
	if err != nil {
		return fmt.Errorf("session working directory is unavailable: %w", err)
	}
	if !savedInfo.IsDir() {
		return errors.New("session working directory must be a directory")
	}
	if requested == "" {
		return nil
	}
	requestedInfo, err := os.Stat(requested)
	if err != nil {
		return fmt.Errorf("requested working directory is unavailable: %w", err)
	}
	if !requestedInfo.IsDir() || !os.SameFile(savedInfo, requestedInfo) {
		return errors.New("resume working directory must match the session's working directory")
	}
	return nil
}

func (a *Agent) resumeRegisteredSession(ctx context.Context, s *Session, workingDir string, additionalDirs []string) error {
	saved, _ := s.workspaceSnapshot()
	if err := validateResumeWorkingDir(saved, workingDir); err != nil {
		return acp.NewInvalidParams(err.Error())
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return errors.New("agent stopped")
	}
	if a.sessions[s.id] != s {
		return errSessionClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	busy := acp.NewInvalidRequest("session has a running or pending prompt; retry resume after it finishes")
	if s.cancel != nil {
		return busy
	}
	s.initTurns()
	select {
	case <-s.turns:
		defer func() { s.turns <- struct{}{} }()
	default:
		// A canceled queued prompt can clear cancel while an earlier turn drains.
		return busy
	}
	s.additionalDirs = slices.Clone(additionalDirs)
	return nil
}
