package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/tools/builtin"
	agenttool "github.com/docker/docker-agent/pkg/tools/builtin/agent"
)

// agentNames returns the names of the given agents.
func agentNames(agents []*agent.Agent) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name()
	}
	return names
}

// validateAgentInList checks that targetAgent appears in the given agent list.
// Returns a tool error result if not found, or nil if the target is valid.
// The action describes the attempted operation (e.g. "transfer task to"),
// and listDesc is a human-readable description of the list (e.g. "sub-agents list").
func validateAgentInList(currentAgent, targetAgent, action, listDesc string, agents []*agent.Agent) *tools.ToolCallResult {
	if slices.ContainsFunc(agents, func(a *agent.Agent) bool { return a.Name() == targetAgent }) {
		return nil
	}
	if names := agentNames(agents); len(names) > 0 {
		return tools.ResultError(fmt.Sprintf(
			"Agent %s cannot %s %s: target agent not in %s. Available agent IDs are: %s",
			currentAgent, action, targetAgent, listDesc, strings.Join(names, ", "),
		))
	}
	return tools.ResultError(fmt.Sprintf(
		"Agent %s cannot %s %s: target agent not in %s. No agents are configured in this list.",
		currentAgent, action, targetAgent, listDesc,
	))
}

// buildTaskSystemMessage constructs the system message for a delegated task.
// attachedFiles, when non-empty, lists absolute paths of files the user
// attached to the parent conversation; they are surfaced to the sub-agent so
// it can use them directly without scanning the workspace or guessing from a
// bare filename.
func buildTaskSystemMessage(task, expectedOutput string, attachedFiles []string) string {
	var b strings.Builder
	b.WriteString("You are a member of a team of agents. Your goal is to complete the following task:")
	fmt.Fprintf(&b, "\n\n<task>\n%s\n</task>", task)
	if expectedOutput != "" {
		fmt.Fprintf(&b, "\n\n<expected_output>\n%s\n</expected_output>", expectedOutput)
	}
	if len(attachedFiles) > 0 {
		b.WriteString("\n\nThe user attached these files in the original conversation. They are available for you to read at these absolute paths; prefer them over any bare filenames mentioned in <task>:\n<attached_files>")
		for _, p := range attachedFiles {
			fmt.Fprintf(&b, "\n- %s", p)
		}
		b.WriteString("\n</attached_files>")
	}
	b.WriteString("\n\nIf the task references files, treat any absolute paths in <task> as authoritative and use them as-is. If a referenced file is given by name only (e.g. \"foo.go\"), do not guess: search the workspace or ask the calling agent for the absolute path before reading or modifying the file.")
	return b.String()
}

// SubSessionConfig describes the shape of a child session: system prompt,
// implicit user message, agent identity, tool approval, exclusions, etc.
// It is the data input to [newSubSession]; the orchestration around
// running such a session (telemetry, current-agent switching, event
// forwarding) lives in [delegator].
type SubSessionConfig struct {
	// Task is the user-facing task description.
	Task string
	// ExpectedOutput is an optional description of what the sub-agent should produce.
	ExpectedOutput string
	// SystemMessage, when non-empty, replaces the default task-based system
	// message. This is used by skill sub-agents whose system prompt is the
	// skill content itself rather than the team delegation boilerplate.
	SystemMessage string
	// AgentName is the name of the agent that will execute the sub-session.
	AgentName string
	// Title is a human-readable label for the sub-session (e.g. "Transferred task").
	Title string
	// ToolsApproved overrides whether tools are pre-approved in the child session.
	ToolsApproved bool
	// PinAgent, when true, pins the child session to AgentName via
	// session.WithAgentName. This is required for concurrent background
	// tasks that must not share the runtime's mutable currentAgent field.
	PinAgent bool
	// ImplicitUserMessage, when non-empty, overrides the default "Please proceed."
	// user message sent to the child session. This allows callers like skill
	// sub-agents to pass the task description as the user message.
	ImplicitUserMessage string
	// ExcludedTools lists tool names that should be filtered out of the agent's
	// tool list for the child session. This prevents recursive tool calls
	// (e.g. run_skill calling itself in a skill sub-session).
	ExcludedTools []string
}

// newSubSession builds a *session.Session from a SubSessionConfig and a parent
// session. It consolidates the session options that were previously duplicated
// across handleTaskTransfer and RunAgent.
func newSubSession(parent *session.Session, cfg SubSessionConfig, childAgent *agent.Agent) *session.Session {
	// Sub-agents start in a fresh session, so they don't see the user's
	// original messages or attached files. Snapshot the parent's attached
	// files once and propagate them both to the system prompt (so the agent
	// is told about them) and to the child session (so further nested
	// transfers keep inheriting them).
	attachedFiles := parent.AttachedFilesSnapshot()

	sysMsg := cfg.SystemMessage
	if sysMsg == "" {
		sysMsg = buildTaskSystemMessage(cfg.Task, cfg.ExpectedOutput, attachedFiles)
	}

	userMsg := cfg.ImplicitUserMessage
	if userMsg == "" {
		userMsg = "Please proceed."
	}

	opts := []session.Opt{
		session.WithSystemMessage(sysMsg),
		session.WithImplicitUserMessage(userMsg),
		session.WithMaxIterations(childAgent.MaxIterations()),
		session.WithMaxConsecutiveToolCalls(childAgent.MaxConsecutiveToolCalls()),
		session.WithMaxOldToolCallTokens(childAgent.MaxOldToolCallTokens()),
		session.WithTitle(cfg.Title),
		session.WithToolsApproved(cfg.ToolsApproved),
		session.WithSendUserMessage(false),
		session.WithParentID(parent.ID),
		session.WithAttachedFiles(attachedFiles),
	}
	if cfg.PinAgent {
		opts = append(opts, session.WithAgentName(cfg.AgentName))
	}
	// Merge parent's excluded tools with config's excluded tools so that
	// nested sub-sessions (e.g. skill → transfer_task → child) inherit
	// exclusions from all ancestors and don't re-introduce filtered tools.
	excludedTools := mergeExcludedTools(parent.ExcludedTools, cfg.ExcludedTools)
	if len(excludedTools) > 0 {
		opts = append(opts, session.WithExcludedTools(excludedTools))
	}
	return session.New(opts...)
}

// mergeExcludedTools combines two excluded-tool lists, deduplicating entries.
// It returns nil when both inputs are empty.
func mergeExcludedTools(parent, child []string) []string {
	if len(parent) == 0 {
		return child
	}
	if len(child) == 0 {
		return parent
	}
	set := make(map[string]struct{}, len(parent)+len(child))
	for _, t := range parent {
		set[t] = struct{}{}
	}
	for _, t := range child {
		set[t] = struct{}{}
	}
	merged := make([]string, 0, len(set))
	for t := range set {
		merged = append(merged, t)
	}
	return merged
}

// CurrentAgentSubAgentNames implements agenttool.Runner.
func (r *LocalRuntime) CurrentAgentSubAgentNames() []string {
	a := r.CurrentAgent()
	if a == nil {
		return nil
	}
	return agentNames(a.SubAgents())
}

// RunAgent implements agenttool.Runner. It starts a sub-agent synchronously and
// blocks until completion or cancellation.
//
// Background tasks run with tools pre-approved because there is no user present
// to respond to interactive approval prompts during async execution. This is a
// deliberate design trade-off: the user implicitly authorises all tool calls
// made by the sub-agent when they approve run_background_agent. Callers should
// be aware that prompt injection in the sub-agent's context could exploit this
// gate-bypass.
//
// TODO: propagate the parent session's per-tool permission rules once the
// runtime supports per-session permission scoping rather than a single shared
// ToolsApproved flag.
func (r *LocalRuntime) RunAgent(ctx context.Context, params agenttool.RunParams) *agenttool.RunResult {
	cfg := SubSessionConfig{
		Task:           params.Task,
		ExpectedOutput: params.ExpectedOutput,
		AgentName:      params.AgentName,
		Title:          "Background agent task",
		ToolsApproved:  true,
		PinAgent:       true,
	}
	return r.delegator.runCollecting(ctx, params.ParentSession, cfg, params.OnContent)
}

func (r *LocalRuntime) handleTaskTransfer(ctx context.Context, sess *session.Session, toolCall tools.ToolCall, evts chan Event) (*tools.ToolCallResult, error) {
	var params struct {
		Agent          string `json:"agent"`
		Task           string `json:"task"`
		ExpectedOutput string `json:"expected_output"`
	}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	a := r.CurrentAgent()

	// Validate that the target agent is in the current agent's sub-agents list
	if errResult := validateAgentInList(a.Name(), params.Agent, "transfer task to", "sub-agents list", a.SubAgents()); errResult != nil {
		return errResult, nil
	}

	slog.Debug("Transferring task to agent", "from_agent", a.Name(), "to_agent", params.Agent, "task", params.Task)

	return r.delegator.runForwarding(ctx, sess, evts, delegationRequest{
		SubSessionConfig: SubSessionConfig{
			Task:           params.Task,
			ExpectedOutput: params.ExpectedOutput,
			AgentName:      params.Agent,
			Title:          "Transferred task",
			ToolsApproved:  sess.ToolsApproved,
		},
		SwitchCurrentAgent: true,
		SpanName:           "runtime.task_transfer",
		SpanAttributes: []attribute.KeyValue{
			attribute.String("from.agent", a.Name()),
			attribute.String("to.agent", params.Agent),
			attribute.String("session.id", sess.ID),
		},
	})
}

func (r *LocalRuntime) handleHandoff(ctx context.Context, sess *session.Session, toolCall tools.ToolCall, _ chan Event) (*tools.ToolCallResult, error) {
	var params builtin.HandoffArgs
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	ca := r.CurrentAgentName()
	currentAgent, err := r.team.Agent(ca)
	if err != nil {
		return nil, fmt.Errorf("current agent not found: %w", err)
	}

	// Validate that the target agent is in the current agent's handoffs list
	if errResult := validateAgentInList(ca, params.Agent, "hand off to", "handoffs list", currentAgent.Handoffs()); errResult != nil {
		return errResult, nil
	}

	next, err := r.team.Agent(params.Agent)
	if err != nil {
		return nil, err
	}

	r.executeOnAgentSwitchHooks(ctx, currentAgent, sess.ID, ca, next.Name(), agentSwitchKindHandoff)
	r.setCurrentAgent(next.Name())
	handoffMessage := "The agent " + ca + " handed off the conversation to you. " +
		"Your available handoff agents and tools are specified in the system messages that follow. " +
		"Only use those capabilities - do not attempt to use tools or hand off to agents that you see " +
		"in the conversation history from previous agents, as those were available to different agents " +
		"with different capabilities. Look at the conversation history for context, but only use the " +
		"handoff agents and tools that are listed in your system messages below. " +
		"Complete your part of the task and hand off to the next appropriate agent in your workflow " +
		"(if any are available to you), or respond directly to the user if you are the final agent."
	return tools.ResultSuccess(handoffMessage), nil
}
