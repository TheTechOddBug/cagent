package runtime

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
	agenttool "github.com/docker/docker-agent/pkg/tools/builtin/agent"
)

// delegationRequest is the runtime-private description of a request to
// spawn a child session. It bundles the child-session shape
// (SubSessionConfig) with the orchestration knobs the delegator needs:
// telemetry span identity and whether to swap the runtime's current
// agent for the duration of the call.
//
// Adding a new "spawn a sub-agent" feature is a matter of building one
// of these and calling [delegator.runForwarding] (or
// [delegator.runCollecting]); none of the boilerplate around spans,
// AgentInfo events, agent restoration, or event forwarding leaks into
// the caller.
type delegationRequest struct {
	// SubSessionConfig describes the child-session shape (system prompt,
	// title, tool approval, excluded tools, ...). See its docstring.
	SubSessionConfig

	// SwitchCurrentAgent, when true, swaps r.currentAgent to
	// SubSessionConfig.AgentName for the lifetime of the call and emits
	// AgentSwitching/AgentInfo events on entry and exit. Used by
	// transfer_task. Mutually exclusive in spirit with PinAgent: pinning
	// is for concurrent sub-sessions that must NOT share the runtime's
	// mutable currentAgent, while switching is for sequential delegations
	// where the parent loop is blocked anyway.
	SwitchCurrentAgent bool

	// SpanName is the OpenTelemetry span name (e.g. "runtime.task_transfer").
	SpanName string
	// SpanAttributes are extra attributes attached to the span on creation.
	SpanAttributes []attribute.KeyValue
}

// delegator orchestrates the lifecycle of sub-agent sessions. It is the
// single seam through which transfer_task, run_skill,
// run_background_agent (and any future "spawn a sub-agent" feature)
// build, run, and tear down child sessions consistently.
//
// The delegator holds a back-pointer to its [LocalRuntime] so it can
// reach RunStream, the team, the current-agent state, and the tracer
// without re-injecting them at every callsite. The runtime owns
// exactly one delegator, constructed in [NewLocalRuntime].
type delegator struct {
	rt *LocalRuntime
}

// newDelegator constructs the runtime's single delegator instance.
func newDelegator(rt *LocalRuntime) *delegator {
	return &delegator{rt: rt}
}

// runForwarding runs a child session synchronously, forwarding all of
// its events to evts and propagating tool-approval state back to the
// parent on completion. This is the "interactive" path used by
// transfer_task and run_skill: the parent loop is blocked while the
// child executes, and the user sees the child's events live.
//
// On success it returns a tool result whose output is the child's last
// assistant message. On error it has already forwarded the ErrorEvent
// to evts and returns a wrapped error so the caller can record it on
// its own span.
//
// runForwarding handles every concern the callers used to duplicate:
//
//   - opens a span named req.SpanName with req.SpanAttributes
//   - (optionally) swaps r.currentAgent and emits the
//     AgentSwitching / AgentInfo events on entry and exit
//   - resolves the child agent from the team
//   - constructs the sub-session via [newSubSession]
//   - drives [LocalRuntime.RunStream] and forwards events
//   - records the sub-session on the parent and emits SubSessionCompleted
func (d *delegator) runForwarding(ctx context.Context, parent *session.Session, evts chan Event, req delegationRequest) (*tools.ToolCallResult, error) {
	ctx, span := d.rt.startSpan(ctx, req.SpanName, trace.WithAttributes(req.SpanAttributes...))
	defer span.End()

	callerAgentName := d.rt.CurrentAgentName()
	parentAgent, err := d.rt.team.Agent(callerAgentName)
	if err != nil {
		return nil, fmt.Errorf("current agent not found: %w", err)
	}

	if req.SwitchCurrentAgent {
		evts <- AgentSwitching(true, callerAgentName, req.AgentName)
		d.rt.executeOnAgentSwitchHooks(ctx, parentAgent, parent.ID, callerAgentName, req.AgentName, agentSwitchKindTransferTask)
		d.rt.setCurrentAgent(req.AgentName)
		defer func() {
			d.rt.setCurrentAgent(callerAgentName)
			evts <- AgentSwitching(false, req.AgentName, callerAgentName)
			d.rt.executeOnAgentSwitchHooks(ctx, parentAgent, parent.ID, req.AgentName, callerAgentName, agentSwitchKindTransferTaskReturn)
			// Restore original agent info in sidebar.
			evts <- AgentInfo(parentAgent.Name(), getAgentModelID(parentAgent), parentAgent.Description(), parentAgent.WelcomeMessage())
		}()
	}

	child, err := d.rt.team.Agent(req.AgentName)
	if err != nil {
		return nil, err
	}

	if req.SwitchCurrentAgent {
		// Notify the sidebar that the child agent is now in charge.
		evts <- AgentInfo(child.Name(), getAgentModelID(child), child.Description(), child.WelcomeMessage())
	}

	s := newSubSession(parent, req.SubSessionConfig, child)

	childEvents := d.rt.RunStream(ctx, s)
	for event := range childEvents {
		evts <- event
		if errEvent, ok := event.(*ErrorEvent); ok {
			// Drain remaining events (including StreamStoppedEvent) so the
			// TUI's streamDepth counter stays balanced.
			for remaining := range childEvents {
				evts <- remaining
			}
			span.RecordError(fmt.Errorf("%s", errEvent.Error))
			span.SetStatus(codes.Error, "sub-session error")
			return nil, fmt.Errorf("%s", errEvent.Error)
		}
	}

	parent.ToolsApproved = s.ToolsApproved
	parent.AddSubSession(s)
	evts <- SubSessionCompleted(parent.ID, s, callerAgentName)

	span.SetStatus(codes.Ok, "sub-session completed")
	return tools.ResultSuccess(s.GetLastAssistantMessageContent()), nil
}

// runCollecting runs a child session and collects its output via an
// optional content callback instead of forwarding events. This is the
// non-interactive path used by background agents: there's no live UI,
// so events are dropped and only the final assistant message (or the
// first error) matters.
//
// Unlike runForwarding it does not emit AgentSwitching/AgentInfo
// events: callers like background agents PinAgent the child session so
// the runtime never mutates the shared currentAgent state.
func (d *delegator) runCollecting(ctx context.Context, parent *session.Session, cfg SubSessionConfig, onContent func(string)) *agenttool.RunResult {
	child, err := d.rt.team.Agent(cfg.AgentName)
	if err != nil {
		return &agenttool.RunResult{ErrMsg: fmt.Sprintf("agent %q not found: %s", cfg.AgentName, err)}
	}

	s := newSubSession(parent, cfg, child)

	var errMsg string
	events := d.rt.RunStream(ctx, s)
	for event := range events {
		if ctx.Err() != nil {
			break
		}
		if choice, ok := event.(*AgentChoiceEvent); ok && choice.Content != "" {
			if onContent != nil {
				onContent(choice.Content)
			}
		}
		if errEvt, ok := event.(*ErrorEvent); ok {
			errMsg = errEvt.Error
			break
		}
	}
	// Drain remaining events so the RunStream goroutine can complete
	// and close the channel without blocking on a full buffer.
	for range events {
	}

	if errMsg != "" {
		return &agenttool.RunResult{ErrMsg: errMsg}
	}

	result := s.GetLastAssistantMessageContent()
	parent.AddSubSession(s)
	return &agenttool.RunResult{Result: result}
}
