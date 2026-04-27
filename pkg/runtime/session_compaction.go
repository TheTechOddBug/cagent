package runtime

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/compaction"
	"github.com/docker/docker-agent/pkg/model/provider"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
)

const maxSummaryTokens = 16_000

// maxKeepTokens is the maximum number of tokens to preserve from the end of
// the conversation during compaction. These recent messages are kept verbatim
// so the LLM can continue naturally after compaction.
const maxKeepTokens = 20_000

// Compaction reasons reported to BeforeCompaction / AfterCompaction hooks.
const (
	compactionReasonThreshold = "threshold"
	compactionReasonOverflow  = "overflow"
	compactionReasonManual    = "manual"
)

// doCompact runs compaction on a session and applies the result (events,
// persistence, token count updates). The agent is used to extract the
// conversation from the session and to obtain the model for summarization.
//
// reason is one of [compactionReasonThreshold] (proactive 90% trigger),
// [compactionReasonOverflow] (post-overflow recovery) or
// [compactionReasonManual] (user-invoked /compact). It is forwarded to
// BeforeCompaction / AfterCompaction hooks.
//
// Hook integration:
//   - BeforeCompaction fires first. If a hook denies (Decision: "block"),
//     the runtime returns immediately without emitting any compaction
//     events; the conversation is left untouched.
//   - If a BeforeCompaction hook supplies a non-empty Summary in
//     HookSpecificOutput, the runtime applies that summary verbatim and
//     skips the LLM-based summarization entirely.
//   - AfterCompaction fires after the summary has been applied; it is
//     observational.
//
// If no hooks are configured for any of these events, control flow is
// bit-for-bit identical to the previous, hookless implementation.
//
// Note: the runtime does NOT re-fire session_start with Source="compact".
// session_start hook output is held as transient context that is threaded
// into every model call (see [LocalRuntime.executeSessionStartHooks]), so
// env / cwd / OS info is automatically present after a compaction without
// any extra dispatch.
func (r *LocalRuntime) doCompact(ctx context.Context, sess *session.Session, a *agent.Agent, additionalPrompt, reason string, events chan Event) {
	// Build the model used for summarization. CloneWithOptions only
	// allocates a struct, so it's cheap to call early — even when a hook
	// ends up vetoing the compaction.
	summaryModel := provider.CloneWithOptions(ctx, a.Model(),
		options.WithStructuredOutput(nil),
		options.WithMaxTokens(maxSummaryTokens),
	)

	// Best-effort context-limit lookup so before_compaction hooks can
	// decide based on real model pressure. Errors are deferred to the
	// LLM path: a hook that supplies its own summary doesn't need the
	// model definition at all.
	modelDef, modelErr := r.modelsStore.GetModel(ctx, summaryModel.ID())
	var contextLimit int64
	if modelErr == nil && modelDef != nil {
		contextLimit = int64(modelDef.Limit.Context)
	}

	// before_compaction: hooks can veto the compaction or supply a
	// custom summary string in lieu of the LLM call.
	preResult := r.executeBeforeCompactionHooks(ctx, sess, a, reason, contextLimit, events)
	if preResult != nil && !preResult.Allowed {
		slog.Info("Session compaction skipped by before_compaction hook",
			"session_id", sess.ID,
			"agent", a.Name(),
			"reason", reason,
			"hook_message", preResult.Message,
		)
		// Do not emit started/completed runtime events: no compaction
		// occurred. dispatchHook already surfaced any SystemMessage as
		// a Warning event for the user.
		return
	}

	slog.Debug("Generating summary for session", "session_id", sess.ID, "reason", reason)
	events <- SessionCompaction(sess.ID, "started", a.Name())
	defer func() {
		events <- SessionCompaction(sess.ID, "completed", a.Name())
	}()

	compactionAgent := agent.New("root", compaction.SystemPrompt, agent.WithModel(summaryModel))

	var (
		summary        string
		firstKeptEntry int
		compactionCost float64
		newInputTokens int64
	)

	if preResult != nil && preResult.Summary != "" {
		// A hook supplied a custom summary — apply it directly and skip
		// the LLM-based summarization. We still honor the same
		// maxKeepTokens tail-keep policy as the LLM path so the
		// conversation continuity contract is unchanged.
		slog.Debug("Using compaction summary from before_compaction hook",
			"session_id", sess.ID, "agent", a.Name(), "summary_length", len(preResult.Summary))
		summary = preResult.Summary
		firstKeptEntry = computeFirstKeptEntry(sess, compactionAgent)
		// Estimate the summary's token count for session bookkeeping;
		// no LLM was called so the cost is zero.
		newInputTokens = compaction.EstimateMessageTokens(&chat.Message{
			Role:    chat.MessageRoleAssistant,
			Content: summary,
		})
	} else {
		// LLM-based compaction (default path). This block must remain
		// behaviourally identical to the pre-hooks implementation.
		if modelErr != nil || modelDef == nil {
			slog.Error("Failed to generate session summary", "error", errors.New("failed to get model definition"))
			events <- Error("Failed to get model definition")
			return
		}

		// Compute the messages to compact, keeping recent messages aside.
		messages, fke := extractMessagesToCompact(sess, compactionAgent, int64(modelDef.Limit.Context), additionalPrompt, r.now)
		firstKeptEntry = fke

		// Run the compaction.
		compactionSession := session.New(
			session.WithTitle("Generating summary"),
			session.WithMessages(toItems(messages)),
		)

		t := team.New(team.WithAgents(compactionAgent))
		rt, err := New(t, WithSessionCompaction(false))
		if err != nil {
			slog.Error("Failed to generate session summary", "error", err)
			events <- Error(err.Error())
			return
		}
		if _, err = rt.Run(ctx, compactionSession); err != nil {
			slog.Error("Failed to generate session summary", "error", err)
			events <- Error(err.Error())
			return
		}

		summary = compactionSession.GetLastAssistantMessageContent()
		if summary == "" {
			return
		}
		newInputTokens = compactionSession.OutputTokens
		compactionCost = compactionSession.TotalCost()
	}

	// Update the session.
	sess.InputTokens = newInputTokens
	sess.OutputTokens = 0
	sess.Messages = append(sess.Messages, session.Item{
		Summary:        summary,
		FirstKeptEntry: firstKeptEntry,
		Cost:           compactionCost,
	})
	_ = r.sessionStore.UpdateSession(ctx, sess)

	slog.Debug("Generated session summary", "session_id", sess.ID, "summary_length", len(summary))
	events <- SessionSummary(sess.ID, summary, a.Name(), firstKeptEntry)

	// after_compaction: observational. Fired only when a summary was
	// actually applied to the session.
	r.executeAfterCompactionHooks(ctx, sess, a, reason, contextLimit, summary, events)
}

// computeFirstKeptEntry returns the index into sess.Messages of the
// first message preserved verbatim after compaction, given the
// configured maxKeepTokens window. Used by the hook-supplied-summary
// path so the tail-keep policy stays consistent with the LLM path.
func computeFirstKeptEntry(sess *session.Session, compactionAgent *agent.Agent) int {
	var messages []chat.Message
	for _, msg := range sess.GetMessages(compactionAgent) {
		if msg.Role == chat.MessageRoleSystem {
			continue
		}
		messages = append(messages, msg)
	}
	return mapToSessionIndex(sess, splitIndexForKeep(messages, maxKeepTokens))
}

// extractMessagesToCompact returns the messages to send to the compaction model
// and the index (into sess.Messages) of the first message that was kept aside.
// Recent messages (up to maxKeepTokens) are excluded from compaction so they
// can be preserved verbatim in the session after summarization.
func extractMessagesToCompact(sess *session.Session, compactionAgent *agent.Agent, contextLimit int64, additionalPrompt string, now func() time.Time) ([]chat.Message, int) {
	// Add all the existing messages.
	var messages []chat.Message
	for _, msg := range sess.GetMessages(compactionAgent) {
		if msg.Role == chat.MessageRoleSystem {
			continue
		}

		msg.Cost = 0
		msg.CacheControl = false

		messages = append(messages, msg)
	}

	// Split: keep the last N tokens of messages aside so the LLM retains
	// recent context after compaction.
	splitIdx := splitIndexForKeep(messages, maxKeepTokens)
	messagesToCompact := messages[:splitIdx]
	// Compute firstKeptEntry: index into sess.Messages of the first kept message.
	// The kept messages start at splitIdx in the non-system filtered list. We
	// need to map this back to the original sess.Messages index.
	firstKeptEntry := mapToSessionIndex(sess, splitIdx)

	messages = messagesToCompact

	// Prepare the first (system) message.
	systemPromptMessage := chat.Message{
		Role:      chat.MessageRoleSystem,
		Content:   compaction.SystemPrompt,
		CreatedAt: now().Format(time.RFC3339),
	}
	systemPromptMessageLen := compaction.EstimateMessageTokens(&systemPromptMessage)

	// Prepare the last (user) message.
	userPrompt := compaction.UserPrompt
	if additionalPrompt != "" {
		userPrompt += "\n\n" + additionalPrompt
	}
	userPromptMessage := chat.Message{
		Role:      chat.MessageRoleUser,
		Content:   userPrompt,
		CreatedAt: now().Format(time.RFC3339),
	}
	userPromptMessageLen := compaction.EstimateMessageTokens(&userPromptMessage)

	// Truncate the messages so that they fit in the available context limit
	// (minus the expected max length of the summary).
	contextAvailable := max(0, contextLimit-maxSummaryTokens-systemPromptMessageLen-userPromptMessageLen)
	firstIndex := firstMessageToKeep(messages, contextAvailable)
	if firstIndex < len(messages) {
		messages = messages[firstIndex:]
	} else {
		messages = nil
	}

	// Prepend the first (system) message.
	messages = append([]chat.Message{systemPromptMessage}, messages...)

	// Append the last (user) message.
	messages = append(messages, userPromptMessage)

	return messages, firstKeptEntry
}

// splitIndexForKeep returns the index that splits messages into [0:idx] (to
// compact) and [idx:] (to keep). It walks backwards accumulating tokens up to
// maxTokens, snapping to user/assistant boundaries.
func splitIndexForKeep(messages []chat.Message, maxTokens int64) int {
	if len(messages) == 0 {
		return 0
	}

	var tokens int64
	// Walk from the end; find the earliest index whose suffix fits in maxTokens.
	lastValidBoundary := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		tokens += compaction.EstimateMessageTokens(&messages[i])
		if tokens > maxTokens {
			return lastValidBoundary
		}
		role := messages[i].Role
		if role == chat.MessageRoleUser || role == chat.MessageRoleAssistant {
			lastValidBoundary = i
		}
	}
	// All messages fit within maxTokens — don't keep any aside (compact everything).
	return len(messages)
}

// mapToSessionIndex maps an index in the non-system-filtered message list back
// to the corresponding index in sess.Messages. It counts only message items
// that are not system messages.
func mapToSessionIndex(sess *session.Session, filteredIdx int) int {
	count := 0
	for i, item := range sess.Messages {
		if item.IsMessage() && item.Message.Message.Role != chat.MessageRoleSystem {
			if count == filteredIdx {
				return i
			}
			count++
		}
	}
	// filteredIdx is past the end — no messages to keep.
	return len(sess.Messages)
}

func firstMessageToKeep(messages []chat.Message, contextLimit int64) int {
	var tokens int64

	lastValidMessageSeen := len(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		tokens += compaction.EstimateMessageTokens(&messages[i])
		if tokens > contextLimit {
			return lastValidMessageSeen
		}

		role := messages[i].Role
		if role == chat.MessageRoleUser || role == chat.MessageRoleAssistant {
			lastValidMessageSeen = i
		}
	}

	return lastValidMessageSeen
}

func toItems(messages []chat.Message) []session.Item {
	var items []session.Item

	for _, message := range messages {
		items = append(items, session.Item{
			Message: &session.Message{
				Message: message,
			},
		})
	}

	return items
}
