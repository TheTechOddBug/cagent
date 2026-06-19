// Package embeddedchat provides a small headless chat wrapper around the
// docker-agent runtime for embedders that want to drive an agent from their
// own UI instead of running docker-agent's Bubble Tea application.
package embeddedchat

import (
	"context"
	"errors"
	"fmt"
	"sync"

	dagentcfg "github.com/docker/docker-agent/pkg/config"
	dagentruntime "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/teamloader"
	"github.com/docker/docker-agent/pkg/tools"
)

// Config describes an embedded agent session.
type Config struct {
	// AgentSource is the agent/team definition to load. Bytes sources are a
	// good fit for embedders that ship a pinned agent in their binary.
	AgentSource dagentcfg.Source
	// RuntimeConfig is passed to the team loader. When nil, a zero runtime
	// config is used.
	RuntimeConfig *dagentcfg.RuntimeConfig
	// ToolsetRegistry resolves toolsets declared by AgentSource. When nil,
	// docker-agent's default registry is used.
	ToolsetRegistry teamloader.ToolsetRegistry
	// RuntimeOptions are appended when constructing the runtime.
	RuntimeOptions []dagentruntime.Opt
	// SessionOptions are appended when constructing each conversation session.
	SessionOptions []session.Opt
}

// Event is the UI-friendly form of one runtime stream event.
type Event struct {
	// Text is an assistant text delta.
	Text string
	// Tool describes a tool call starting, awaiting confirmation, or finishing.
	Tool *ToolActivity
	// Err is a user-facing runtime error.
	Err error
	// Done marks a clean end of the reply stream.
	Done bool
	// RuntimeEvent is the original docker-agent runtime event for callers that
	// need lower-level details not projected above.
	RuntimeEvent dagentruntime.Event
}

// ToolActivity describes one tool call surfaced by the runtime.
type ToolActivity struct {
	Call     tools.ToolCall
	Def      tools.Tool
	Finished bool
	IsError  bool
	// NeedsConfirmation is true when the runtime is blocked until Confirm is
	// called with the user's decision.
	NeedsConfirmation bool
}

// runtimeRunner is the subset of runtime.Runtime the headless session needs.
type runtimeRunner interface {
	RunStream(context.Context, *session.Session) <-chan dagentruntime.Event
	Resume(context.Context, dagentruntime.ResumeRequest)
	ResumeElicitation(context.Context, tools.ElicitationAction, map[string]any) error
	Close() error
}

// Session owns one embedded runtime and one mutable conversation session.
type Session struct {
	cfg Config

	rt      runtimeRunner
	session *session.Session
	welcome string

	mu           sync.Mutex
	activeCancel context.CancelFunc
	activeRun    int
}

// New loads the configured agent and creates a fresh conversation session.
func New(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.AgentSource == nil {
		return nil, errors.New("embeddedchat: agent source is required")
	}
	runConfig := cfg.RuntimeConfig
	if runConfig == nil {
		runConfig = &dagentcfg.RuntimeConfig{}
	}

	var loadOpts []teamloader.Opt
	if cfg.ToolsetRegistry != nil {
		loadOpts = append(loadOpts, teamloader.WithToolsetRegistry(cfg.ToolsetRegistry))
	}
	loaded, err := teamloader.LoadWithConfig(ctx, cfg.AgentSource, runConfig, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("embeddedchat: load agent: %w", err)
	}

	modelSwitcher := &dagentruntime.ModelSwitcherConfig{
		Models:             loaded.Models,
		Providers:          loaded.Providers,
		ModelsGateway:      runConfig.ModelsGateway,
		EnvProvider:        runConfig.EnvProvider(),
		AgentDefaultModels: loaded.AgentDefaultModels,
	}

	runtimeOpts := []dagentruntime.Opt{
		dagentruntime.WithModelSwitcherConfig(modelSwitcher),
		dagentruntime.WithWorkingDir(runConfig.WorkingDir),
		dagentruntime.WithSessionStore(session.NewInMemorySessionStore()),
	}
	runtimeOpts = append(runtimeOpts, cfg.RuntimeOptions...)
	rt, err := dagentruntime.New(loaded.Team, runtimeOpts...)
	if err != nil {
		return nil, fmt.Errorf("embeddedchat: create runtime: %w", err)
	}

	s := &Session{cfg: cfg, rt: rt}
	if root, err := loaded.Team.DefaultAgent(); err == nil {
		s.welcome = root.WelcomeMessage()
	}
	s.resetConversationLocked()
	return s, nil
}

// WelcomeMessage returns the loaded agent's welcome message.
func (s *Session) WelcomeMessage() string { return s.welcome }

// Runtime returns the underlying docker-agent runtime for advanced embedders.
// It returns nil only for sessions not created by New.
func (s *Session) Runtime() dagentruntime.Runtime {
	rt, _ := s.rt.(dagentruntime.Runtime)
	return rt
}

// Conversation returns the underlying docker-agent session.
func (s *Session) Conversation() *session.Session { return s.session }

// Restart cancels any active run and replaces the conversation with a fresh
// session, preserving the runtime and loaded agent.
func (s *Session) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelActiveLocked()
	s.resetConversationLocked()
}

// Close cancels any active run and releases runtime resources.
func (s *Session) Close() error {
	s.mu.Lock()
	s.cancelActiveLocked()
	s.mu.Unlock()
	if s.rt != nil {
		return s.rt.Close()
	}
	return nil
}

func (s *Session) resetConversationLocked() {
	opts := append([]session.Opt(nil), s.cfg.SessionOptions...)
	s.session = session.New(opts...)
}

func (s *Session) cancelActiveLocked() {
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
	}
}

// Send appends prompt to the conversation and streams the assistant reply.
// The returned channel is closed after either a Done event or an Err event.
// If ctx is cancelled, Send drains the runtime stream until it stops, but no
// further events are delivered to the caller.
func (s *Session) Send(ctx context.Context, prompt string) (<-chan Event, error) {
	if s.rt == nil || s.session == nil {
		return nil, errors.New("embeddedchat: session is not initialized")
	}

	s.mu.Lock()
	if s.activeCancel != nil {
		s.mu.Unlock()
		return nil, errors.New("embeddedchat: a run is already active")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.activeCancel = cancel
	s.activeRun++
	runID := s.activeRun
	s.session.AddMessage(session.UserMessage(prompt))
	sess := s.session
	rt := s.rt
	s.mu.Unlock()

	out := make(chan Event)
	go s.forwardEvents(runCtx, rt.RunStream(runCtx, sess), out, cancel, runID)
	return out, nil
}

// Confirm answers the pending tool confirmation, if any.
func (s *Session) Confirm(ctx context.Context, req dagentruntime.ResumeRequest) {
	if s.rt == nil {
		return
	}
	s.rt.Resume(ctx, req)
}

func (s *Session) forwardEvents(ctx context.Context, events <-chan dagentruntime.Event, out chan<- Event, cancel context.CancelFunc, runID int) {
	defer close(out)
	defer cancel()
	defer func() {
		s.mu.Lock()
		if s.activeRun == runID {
			s.activeCancel = nil
		}
		s.mu.Unlock()
	}()

	emit := func(e Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	errSent := false
	for event := range events {
		if ctx.Err() != nil {
			continue
		}

		switch e := event.(type) {
		case *dagentruntime.ToolCallConfirmationEvent:
			if errSent {
				s.rt.Resume(ctx, dagentruntime.ResumeReject("The run was aborted."))
				continue
			}
			if !emit(Event{RuntimeEvent: event, Tool: &ToolActivity{Call: e.ToolCall, Def: e.ToolDefinition, NeedsConfirmation: true}}) {
				s.rt.Resume(ctx, dagentruntime.ResumeReject("The run was aborted."))
			}
		case *dagentruntime.ElicitationRequestEvent:
			// This headless wrapper has no built-in elicitation UI. Decline so the
			// run cannot hang forever; embedders that need elicitation can consume
			// RuntimeEvent directly by driving the runtime themselves.
			_ = s.rt.ResumeElicitation(ctx, "decline", nil)
		case *dagentruntime.MaxIterationsReachedEvent:
			s.rt.Resume(ctx, dagentruntime.ResumeReject(""))
		case *dagentruntime.ErrorEvent:
			if errSent {
				continue
			}
			if !emit(Event{RuntimeEvent: event, Err: errors.New(e.Error)}) {
				return
			}
			errSent = true
		default:
			if errSent {
				continue
			}
			if translated, ok := TranslateRuntimeEvent(event); ok {
				if !emit(translated) {
					return
				}
			}
		}
	}
	if !errSent && ctx.Err() == nil {
		emit(Event{Done: true})
	}
}

// TranslateRuntimeEvent translates content-bearing runtime events into the
// compact Event shape used by embedded chat UIs.
func TranslateRuntimeEvent(event dagentruntime.Event) (Event, bool) {
	switch e := event.(type) {
	case *dagentruntime.AgentChoiceEvent:
		if e.Content == "" {
			return Event{}, false
		}
		return Event{RuntimeEvent: event, Text: e.Content}, true
	case *dagentruntime.ToolCallEvent:
		return Event{RuntimeEvent: event, Tool: &ToolActivity{Call: e.ToolCall, Def: e.ToolDefinition}}, true
	case *dagentruntime.ToolCallResponseEvent:
		return Event{RuntimeEvent: event, Tool: &ToolActivity{
			Call:     tools.ToolCall{ID: e.ToolCallID},
			Def:      e.ToolDefinition,
			Finished: true,
			IsError:  e.Result != nil && e.Result.IsError,
		}}, true
	}
	return Event{}, false
}
