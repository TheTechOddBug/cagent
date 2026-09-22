package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/evaluator"
	evaluatorprovider "github.com/docker/docker-agent/pkg/evaluator/provider"
	"github.com/docker/docker-agent/pkg/hooks"
	"github.com/docker/docker-agent/pkg/modelsdev"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/session/sqlitestore"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/tools"
)

func evaluationTestAgent(client evaluator.Evaluator, decision string) (*agent.Agent, *team.Team) {
	root := agent.New("root", "test", agent.WithModel(&mockProvider{id: "test/model"}), agent.WithHooks(&latest.HooksConfig{
		ToolGuard: []latest.HookMatcherConfig{{Hooks: []latest.HookDefinition{{
			Type: hooks.HookTypeEvaluator, Evaluator: "safety",
			EvaluatorPolicy: &latest.EvaluatorPolicy{Decisions: map[string]string{"true": decision}, MinProbability: 0.9, Fallback: "deny"},
		}}}},
	}))
	return root, team.New(team.WithAgents(root), team.WithEvaluators(map[string]evaluator.Evaluator{"safety": client}))
}

func evaluationItems(sess *session.Session) []*session.Evaluation {
	var records []*session.Evaluation
	for _, item := range sess.MessagesSnapshot() {
		if item.Evaluation != nil {
			records = append(records, item.Evaluation)
		}
	}
	return records
}

func TestEvaluatorUsageAccounting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, decision, body string
		price                *latest.CostConfig
		wantCost             *float64
		wantTokens           int64
		wantError            bool
	}{
		{name: "allow", decision: "allow", wantTokens: 15, price: &latest.CostConfig{Input: 1, Output: 2}, wantCost: new(18e-6)},
		{name: "ask", decision: "ask", wantTokens: 15, price: &latest.CostConfig{Input: 1, Output: 2}, wantCost: new(18e-6)},
		{name: "deny", decision: "deny", wantTokens: 15, price: &latest.CostConfig{Input: 1, Output: 2}, wantCost: new(18e-6)},
		{name: "invalid answer retains usage", decision: "allow", body: `{"model":"resolved","answers":[],"usage":{"input_tokens":12,"output_tokens":3}}`, wantTokens: 15, price: &latest.CostConfig{Input: 1, Output: 2}, wantCost: new(18e-6), wantError: true},
		{name: "unknown price", decision: "allow", wantTokens: 15},
		{name: "free", decision: "allow", wantTokens: 15, price: &latest.CostConfig{}, wantCost: new(float64)},
		{name: "missing usage", decision: "allow", body: `{"model":"resolved","answers":{"evaluation":{"type":"noul","noul":1}}}`, price: &latest.CostConfig{Input: 1}},
		{name: "explicit zero", decision: "allow", body: `{"model":"resolved","answers":{"evaluation":{"type":"noul","noul":1}},"usage":{"input_tokens":0,"output_tokens":0}}`, price: &latest.CostConfig{Input: 1}, wantCost: new(float64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := tc.body
			if body == "" {
				body = `{"model":"resolved","answers":{"evaluation":{"type":"noul","noul":1}},"usage":{"input_tokens":12,"output_tokens":3}}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			t.Cleanup(server.Close)
			client, err := evaluatorprovider.New(t.Context(), latest.EvaluatorConfig{
				Provider: "typesafe", Model: "jev-latest", BaseURL: server.URL, Type: "boolean", Instructions: "Assess", Cost: tc.price,
			}, environment.NewMapEnvProvider(map[string]string{"TYPESAFE_API_KEY": "token"}))
			require.NoError(t, err)
			root, tm := evaluationTestAgent(client, tc.decision)
			telemetry := &recordingTelemetry{}
			rt, err := NewLocalRuntime(t.Context(), tm, WithTelemetry(telemetry), WithBudget(&latest.BudgetConfig{MaxCost: 1}),
				WithNamedBudgets(map[string]latest.BudgetConfig{"work": {MaxTokens: 100}}, map[string][]string{"root": {"work"}}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = rt.Close() })
			rt.ensureBudget()
			sess := session.New()
			sess.SetUsage(50, 5)
			sink := &collectSink{}
			ctx := context.WithValue(t.Context(), evaluatorAccountingKey{}, &evaluatorAccounting{r: rt, sess: sess, a: root, events: sink})
			result := rt.dispatchHook(ctx, root, hooks.EventToolGuard, &hooks.Input{SessionID: sess.ID, ToolName: "shell"}, sink)
			require.NotNil(t, result)
			assert.Equal(t, tc.wantError, result.ExitCode != 0)
			records := evaluationItems(sess)
			require.Len(t, records, 1)
			record := records[0]
			assert.Equal(t, "root", record.AgentName)
			assert.Equal(t, "safety", record.Evaluator)
			assert.Equal(t, "resolved", record.Model)
			assert.NotEmpty(t, record.ID)
			assert.Equal(t, tc.wantCost, record.Cost)
			input, output := sess.Usage()
			assert.Equal(t, int64(50), input)
			assert.Equal(t, int64(5), output)
			var wantCost float64
			if tc.wantCost != nil {
				wantCost = *tc.wantCost
			}
			assert.InDelta(t, wantCost, sess.TotalCost(), 1e-12)
			for _, tracker := range rt.currentBudget().all() {
				snapshot := tracker.Tracker.snapshot()
				assert.Equal(t, tc.wantTokens, snapshot.Tokens)
				assert.InDelta(t, wantCost, snapshot.Cost, 1e-12)
				assert.Equal(t, tc.wantCost == nil, snapshot.Unpriced)
			}
			var usage *TokenUsageEvent
			var evaluationEvents int
			for _, event := range sink.events {
				switch e := event.(type) {
				case *TokenUsageEvent:
					usage = e
				case *EvaluationUsageEvent:
					evaluationEvents++
				}
			}
			assert.Equal(t, 1, evaluationEvents)
			require.NotNil(t, usage)
			assert.Equal(t, int64(55), usage.Usage.ContextLength)
			assert.InDelta(t, wantCost, usage.Usage.Cost, 1e-12)
			assert.Nil(t, usage.Usage.LastMessage)
			if record.Usage != nil {
				require.Len(t, telemetry.snapshot().tokenUsages, 1)
				assert.Equal(t, tc.wantTokens, telemetry.snapshot().tokenUsages[0].InputTokens+telemetry.snapshot().tokenUsages[0].OutputTokens)
			}
		})
	}
}

func TestEvaluatorBudgetAdmissionAndBlocking(t *testing.T) {
	t.Parallel()
	for _, tokens := range []bool{false, true} {
		t.Run(strconv.FormatBool(tokens), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int64
			client := runtimeEvaluatorFunc(func(ctx context.Context, _ any) (*evaluator.Result, error) {
				requests.Add(1)
				evaluator.ObserveUsage(ctx, evaluator.UsageRecord{Model: "jev", Usage: &evaluator.Usage{InputTokens: 12, OutputTokens: 3}, Cost: new(0.25)})
				return &evaluator.Result{Type: "boolean", Probability: new(1.0)}, nil
			})
			root, tm := evaluationTestAgent(client, "allow")
			budget := &latest.BudgetConfig{MaxCost: 0.1}
			if tokens {
				budget = &latest.BudgetConfig{MaxTokens: 10}
			}
			rt, err := NewLocalRuntime(t.Context(), tm, WithBudget(budget))
			require.NoError(t, err)
			t.Cleanup(func() { _ = rt.Close() })
			sess := session.New(session.WithSafetyPolicy(session.SafetyPolicyAutonomous), session.WithNonInteractive(true))
			var executed bool
			tool := recordingTool("the_tool", &executed)
			calls := []tools.ToolCall{{ID: "call", Function: tools.FunctionCall{Name: "the_tool", Arguments: `{}`}}}
			sink := &collectSink{}
			stopped, _ := rt.processToolCalls(t.Context(), sess, root, calls, tool, sink)
			assert.True(t, stopped)
			assert.False(t, executed, "exhausting the budget must not bypass the guard")
			assert.EqualValues(t, 1, requests.Load())
			stopped, _ = rt.processToolCalls(t.Context(), sess, root, calls, tool, sink)
			assert.True(t, stopped)
			assert.EqualValues(t, 1, requests.Load(), "no further paid evaluation after exhaustion")
			require.Len(t, evaluationItems(sess), 1)
			assert.InDelta(t, 0.25, sess.TotalCost(), 1e-12)
		})
	}
}

func TestEvaluatorBudgetStopsRun(t *testing.T) {
	t.Parallel()
	client := runtimeEvaluatorFunc(func(ctx context.Context, _ any) (*evaluator.Result, error) {
		evaluator.ObserveUsage(ctx, evaluator.UsageRecord{Model: "jev", Usage: &evaluator.Usage{InputTokens: 10}, Cost: new(0.25)})
		return &evaluator.Result{Type: "boolean", Probability: new(1.0)}, nil
	})
	root, _ := evaluationTestAgent(client, "allow")
	var executed bool
	tool := recordingTool("the_tool", &executed)
	prov := &queueProvider{id: "test/model", streams: []chat.MessageStream{
		newStreamBuilder().AddToolCallName("call", "the_tool").AddToolCallArguments("call", `{}`).AddToolCallStopWithUsage(100, 50).Build(),
		newStreamBuilder().AddContent("must not run").AddStopWithUsage(100, 50).Build(),
	}}
	// Use the existing agent options to preserve evaluator bindings and hook configuration.
	root = agent.New("root", "test", agent.WithHooks(root.Hooks()), agent.WithModel(prov), agent.WithToolSets(newStubToolSet(nil, tool, nil)))
	tm := team.New(team.WithAgents(root), team.WithEvaluators(map[string]evaluator.Evaluator{"safety": client}))
	rt, err := NewLocalRuntime(t.Context(), tm, WithSessionCompaction(false), WithBudget(&latest.BudgetConfig{MaxCost: 0.1}),
		WithModelStore(mockModelStoreWithCostAndLimit{limit: 100_000, cost: modelsdev.Cost{}}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	sess := session.New(session.WithUserMessage("run"), session.WithTitle("test"), session.WithSafetyPolicy(session.SafetyPolicyAutonomous), session.WithNonInteractive(true))
	var stops int
	for event := range rt.RunStream(t.Context(), sess) {
		if _, ok := event.(*BudgetExceededEvent); ok {
			stops++
		}
		if e, ok := event.(*ErrorEvent); ok {
			t.Errorf("unexpected runtime error: %s", e.Error)
		}
	}
	assert.Equal(t, 1, stops)
	assert.False(t, executed)
	assert.Len(t, prov.streams, 1)
	assert.InDelta(t, 0.25, sess.TotalCost(), 1e-12)
}

func TestEvaluatorUsageConcurrentSessions(t *testing.T) {
	t.Parallel()
	client := runtimeEvaluatorFunc(func(ctx context.Context, state any) (*evaluator.Result, error) {
		tokens := state.(int64)
		evaluator.ObserveUsage(ctx, evaluator.UsageRecord{Model: "jev", Usage: &evaluator.Usage{InputTokens: tokens}, Cost: new(float64(tokens) / 1e6)})
		return &evaluator.Result{Type: "boolean", Probability: new(1.0)}, nil
	})
	root, tm := evaluationTestAgent(client, "allow")
	rt, err := NewLocalRuntime(t.Context(), tm, WithBudget(&latest.BudgetConfig{MaxTokens: 100_000}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	rt.ensureBudget()
	wrapper := &accountedEvaluator{client: client, name: "safety"}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			sess := session.New()
			ch := make(chan Event, 100)
			ctx := context.WithValue(t.Context(), evaluatorAccountingKey{}, &evaluatorAccounting{r: rt, sess: sess, a: root, events: NewChannelSink(ch)})
			var calls sync.WaitGroup
			for range 5 {
				calls.Go(func() { _, err := wrapper.Evaluate(ctx, int64(i+1)); assert.NoError(t, err) })
			}
			calls.Wait()
			close(ch)
			assert.Len(t, evaluationItems(sess), 5)
			assert.InDelta(t, float64(5*(i+1))/1e6, sess.TotalCost(), 1e-12)
			var last float64
			for event := range ch {
				if e, ok := event.(*TokenUsageEvent); ok {
					assert.GreaterOrEqual(t, e.Usage.Cost, last)
					last = e.Usage.Cost
				}
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int64(275), rt.currentBudget().trackers[runBudgetName].snapshot().Tokens)
}

func TestEvaluatorUsagePersistenceAndSSE(t *testing.T) {
	t.Parallel()
	store, err := sqlitestore.New(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	sess := session.New()
	sess.SetUsage(99, 5)
	observer := newPersistenceObserver(store)
	observer.OnRunStart(t.Context(), sess)
	evaluation := &session.Evaluation{ID: "request", Evaluator: "safety", AgentName: "root", Model: "jev", Usage: &chat.Usage{InputTokens: 12}, Cost: new(0.25), CreatedAt: time.Now().Format(time.RFC3339)}
	event := &EvaluationUsageEvent{Type: "evaluation_usage", SessionID: sess.ID, AgentContext: newAgentContext("root"), Evaluation: evaluation}
	sess.AddEvaluation(evaluation)
	observer.OnEvent(t.Context(), sess, event)
	observer.OnEvent(t.Context(), sess, NewTokenUsageEvent(sess.ID, "root", SessionUsage(sess, 1000)))
	reloaded, err := store.GetSession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, evaluationItems(reloaded), 1)
	assert.Equal(t, evaluation, evaluationItems(reloaded)[0])
	assert.InDelta(t, 0.25, reloaded.TotalCost(), 1e-12)
	in, out := reloaded.Usage()
	assert.Equal(t, int64(99), in)
	assert.Equal(t, int64(5), out)
	other := *event
	other.SessionID = "child"
	observer.OnEvent(t.Context(), sess, &other)
	reloaded, err = store.GetSession(t.Context(), sess.ID)
	require.NoError(t, err)
	assert.Len(t, evaluationItems(reloaded), 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload, err := json.Marshal(event)
		assert.NoError(t, err)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL)
	require.NoError(t, err)
	stream, err := client.StreamSessionEvents(t.Context(), sess.ID)
	require.NoError(t, err)
	var received []*EvaluationUsageEvent
	for e := range stream {
		if usage, ok := e.(*EvaluationUsageEvent); ok {
			received = append(received, usage)
		}
	}
	require.Len(t, received, 1)
	assert.Equal(t, event.Evaluation, received[0].Evaluation)
	assert.Equal(t, event.SessionID, received[0].SessionID)
	assert.Equal(t, event.AgentName, received[0].AgentName)
	assert.True(t, event.Timestamp.Equal(received[0].Timestamp))
}

func TestEvaluatorAccountingPersistsAfterCancellation(t *testing.T) {
	t.Parallel()
	store, err := sqlitestore.New(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	sess := session.New()
	sess.SetUsage(12, 3)
	observer := newPersistenceObserver(store)
	observer.OnRunStart(t.Context(), sess)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e := &session.Evaluation{ID: "request", Model: "jev", Cost: new(0.1)}
	sess.AddEvaluation(e)
	observer.OnEvent(ctx, sess, &EvaluationUsageEvent{SessionID: sess.ID, Evaluation: e})
	observer.OnEvent(ctx, sess, NewTokenUsageEvent(sess.ID, "root", SessionUsage(sess, 1000)))
	got, err := store.GetSession(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, evaluationItems(got), 1)
	assert.InDelta(t, 0.1, got.TotalCost(), 1e-12)
	summaries, err := store.GetSessionSummaries(t.Context())
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.InDelta(t, 0.1, summaries[0].Cost, 1e-12)
}
