package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/llm/llmtest"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/harness/tool/skills"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewSpec_Defaults(t *testing.T) {
	// ReviewSpec with only required fields should compile.
	spec := ReviewSpec{
		Prompt: "review the conversation",
		Tools:  tool.NewRegistry(),
	}
	assert.NotNil(t, spec.Tools)
	assert.Equal(t, "review the conversation", spec.Prompt)
	// Optional fields default to zero.
	assert.Equal(t, 0, spec.MaxTurns)
	assert.Equal(t, time.Duration(0), spec.Timeout)
	assert.Nil(t, spec.OnEvent)
}

func TestReviewResult_Zero(t *testing.T) {
	res := ReviewResult{}
	assert.Empty(t, res.Actions)
	assert.Equal(t, 0, res.ToolCalls)
	assert.Equal(t, 0, res.Turns)
	assert.NoError(t, res.Err)
}

func TestErrReviewRecursion_IsExported(t *testing.T) {
	assert.True(t, errors.Is(ErrReviewRecursion, ErrReviewRecursion))
	assert.NotEmpty(t, ErrReviewRecursion.Error())
}

func TestReviewPrompts_Exported(t *testing.T) {
	assert.NotEmpty(t, ReviewPromptDefault)
	assert.NotEmpty(t, ReviewPromptVerbose)
	// Verbose should be at least as long as default (more guidance).
	assert.GreaterOrEqual(t, len(ReviewPromptVerbose), len(ReviewPromptDefault))
	// Both should reference the canonical action verbs.
	for _, p := range []string{ReviewPromptDefault, ReviewPromptVerbose} {
		assert.Contains(t, p, "memory")
		assert.Contains(t, p, "skill")
	}
}

func TestReview_RecursionGuard(t *testing.T) {
	// A reviewer Runtime has AgentID = "__review__"; Review on it
	// must refuse with ErrReviewRecursion.
	parent := &Runtime{AgentID: reviewerAgentID}
	spec := ReviewSpec{Prompt: "x", Tools: tool.NewRegistry()}
	res := Review(context.Background(), parent, spec)
	assert.ErrorIs(t, res.Err, ErrReviewRecursion)
}

func TestReview_NilToolsRejected(t *testing.T) {
	parent := &Runtime{AgentID: "main"}
	res := Review(context.Background(), parent, ReviewSpec{Prompt: "x", Tools: nil})
	assert.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "Tools")
}

func TestReview_EmptyPromptRejected(t *testing.T) {
	parent := &Runtime{AgentID: "main"}
	res := Review(context.Background(), parent, ReviewSpec{Prompt: "", Tools: tool.NewRegistry()})
	assert.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "Prompt")
}

func TestSnapshotSession_KeyPrefix(t *testing.T) {
	parent := session.NewSession("main-agent", "alpha")
	parent.Append(session.UserMessageEntry("first prompt"))
	parent.Append(session.AssistantMessageEntry("first reply"))

	clone := snapshotSession(parent)
	assert.Equal(t, "main-agent", clone.AgentID, "AgentID should be preserved")
	assert.Equal(t, "review-alpha", clone.Key, "Key should be prefixed with review-")
	// Has the same number of entries.
	assert.Len(t, clone.View(), len(parent.View()))
}

func TestSnapshotSession_DoesNotMutateParent(t *testing.T) {
	parent := session.NewSession("main-agent", "main")
	parent.Append(session.UserMessageEntry("hello"))
	originalCount := len(parent.View())

	clone := snapshotSession(parent)
	clone.Append(session.AssistantMessageEntry("review note"))

	// Parent's entry count must NOT have changed.
	assert.Equal(t, originalCount, len(parent.View()),
		"appending to clone must not affect parent")
	// Clone's entry count is original + 1.
	assert.Equal(t, originalCount+1, len(clone.View()))
}

// fakeMemoryProvider is a minimal MemoryProvider for tests.
type fakeMemoryProvider struct{ index string }

func (f *fakeMemoryProvider) FormatIndex() string                { return f.index }
func (f *fakeMemoryProvider) Get(id string) (string, bool)       { return "", false }

// fakeSkillProvider is a minimal SkillProvider for tests.
type fakeSkillProvider struct{ index string }

func (f *fakeSkillProvider) FormatIndex() string                { return f.index }
func (f *fakeSkillProvider) Get(name string) (string, bool)     { return "", false }

// denyAllPermission is a PermissionChecker that denies everything.
type denyAllPermission struct{}

func (denyAllPermission) Check(ctx context.Context, agentID, toolName string, input json.RawMessage) tool.Decision {
	return tool.Decision{Behavior: tool.DecisionDeny, Reason: "denied by test checker"}
}
func (denyAllPermission) FilterToolDefs(defs []llm.ToolDef, agentID string) []llm.ToolDef {
	return nil
}

// newReviewTestParent constructs a minimal parent Runtime suitable for
// build/run tests.
func newReviewTestParent(t *testing.T) *Runtime {
	t.Helper()
	return &Runtime{
		AgentID:   "main-agent",
		AgentName: "Main",
		Provider:  "anthropic",
		Model:     "claude-haiku-4-5",
		Workspace: t.TempDir(),
		Session:   session.NewSession("main-agent", "main"),
		LLM:       &llmtest.Stub{Text: "ok"},
	}
}

func TestBuildReviewerRuntime_AgentIDIsReviewer(t *testing.T) {
	parent := newReviewTestParent(t)
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x",
		Tools:  tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.Equal(t, reviewerAgentID, rt.AgentID)
}

func TestBuildReviewerRuntime_InheritsParentLLMAndModel(t *testing.T) {
	parent := newReviewTestParent(t)
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x", Tools: tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.Same(t, parent.LLM, rt.LLM, "LLM should inherit from parent when spec.Provider is nil")
	assert.NotEmpty(t, rt.Model, "Model should be inherited and parsed")
}

func TestBuildReviewerRuntime_InheritsMemoryAndSkills(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.Memory = &fakeMemoryProvider{index: "from parent memory"}
	parent.Skills = &fakeSkillProvider{index: "from parent skills"}
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x", Tools: tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.NotNil(t, rt.Memory, "Memory should inherit from parent")
	assert.NotNil(t, rt.Skills, "Skills should inherit from parent")
}

func TestBuildReviewerRuntime_HooksAreZeroed(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.AgentLoop.Hooks.OnStop = func(ctx context.Context, reason string) {
		t.Errorf("parent OnStop should NOT propagate to reviewer")
	}
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x", Tools: tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.Nil(t, rt.AgentLoop.Hooks.OnStop)
	assert.Nil(t, rt.AgentLoop.Hooks.OnUserPromptSubmit)
	assert.Nil(t, rt.AgentLoop.Hooks.BeforeToolUse)
	assert.Nil(t, rt.AgentLoop.Hooks.AfterToolUse)
	assert.Nil(t, rt.AgentLoop.Hooks.OnSessionStart)
}

func TestBuildReviewerRuntime_PermissionDefaultsToNil(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.Permission = denyAllPermission{}
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x", Tools: tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.Nil(t, rt.Permission, "reviewer Permission should default to nil (allow-all)")
}

func TestBuildReviewerRuntime_IngestSourceIsReview(t *testing.T) {
	parent := newReviewTestParent(t)
	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt: "x", Tools: tool.NewRegistry(),
	})
	require.NoError(t, err)
	assert.Equal(t, "review", rt.IngestSource, `reviewer IngestSource must be "review" so KG ingest skips it`)
}

// statefulReviewMock embeds llmtest.Base so we satisfy the LLMProvider
// interface even as it grows; only ChatStream is custom.
type statefulReviewMock struct {
	llmtest.Base
	responses [][]llm.ChatEvent
	callCount int
}

func (m *statefulReviewMock) ChatStream(_ context.Context, _ llm.ChatRequest) (<-chan llm.ChatEvent, error) {
	idx := m.callCount
	m.callCount++
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	events := m.responses[idx]
	ch := make(chan llm.ChatEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// reviewNoopTool emits a canonical action-result envelope so action
// extraction (Task 7) has something to find.
type reviewNoopTool struct{ name string }

func (n *reviewNoopTool) Name() string                             { return n.name }
func (n *reviewNoopTool) Description() string                      { return "noop for review tests" }
func (n *reviewNoopTool) Parameters() json.RawMessage              { return json.RawMessage(`{"type":"object"}`) }
func (n *reviewNoopTool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (n *reviewNoopTool) Execute(_ context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	return tool.ToolResult{Output: `{"success":true,"message":"ok","target":"test"}`}, nil
}

func TestReview_DrainsEventsSilently(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.LLM = &statefulReviewMock{
		responses: [][]llm.ChatEvent{
			// Single response: text then done.
			{
				{Type: llm.EventTextDelta, Text: "review complete"},
				{Type: llm.EventDone},
			},
		},
	}

	res := Review(context.Background(), parent, ReviewSpec{
		Prompt: "review please",
		Tools:  tool.NewRegistry(),
	})
	// Setup must succeed (no Err for guards).
	assert.NoError(t, res.Err, "Review should not error on happy path")
	assert.GreaterOrEqual(t, res.Turns, 1, "Turns counter should be populated")
	assert.Greater(t, res.Duration, time.Duration(0), "Duration must be populated")
}

func TestReview_OnEventReceivesEvents(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.LLM = &statefulReviewMock{
		responses: [][]llm.ChatEvent{
			{
				{Type: llm.EventTextDelta, Text: "hi"},
				{Type: llm.EventDone},
			},
		},
	}

	var captured []AgentEvent
	res := Review(context.Background(), parent, ReviewSpec{
		Prompt: "x",
		Tools:  tool.NewRegistry(),
		OnEvent: func(ev AgentEvent) {
			captured = append(captured, ev)
		},
	})
	assert.NoError(t, res.Err)
	require.NotEmpty(t, captured, "OnEvent should have received at least one event")

	var sawDone bool
	for _, ev := range captured {
		if ev.Type == EventDone {
			sawDone = true
			break
		}
	}
	assert.True(t, sawDone, "OnEvent should have received EventDone")
}

func TestReview_CountsToolCalls(t *testing.T) {
	parent := newReviewTestParent(t)
	parent.LLM = &statefulReviewMock{
		responses: [][]llm.ChatEvent{
			// First call: emit a tool call to "noop".
			{
				{Type: llm.EventToolCallStart, ToolCall: &llm.ToolCall{ID: "tc_1", Name: "noop"}},
				{Type: llm.EventToolCallDone, ToolCall: &llm.ToolCall{ID: "tc_1", Name: "noop", Input: json.RawMessage(`{}`)}},
				{Type: llm.EventDone},
			},
			// Second call: finish with text.
			{
				{Type: llm.EventTextDelta, Text: "done"},
				{Type: llm.EventDone},
			},
		},
	}

	reg := tool.NewRegistry()
	reg.Register(&reviewNoopTool{name: "noop"})

	res := Review(context.Background(), parent, ReviewSpec{
		Prompt: "x",
		Tools:  reg,
	})
	assert.NoError(t, res.Err)
	assert.GreaterOrEqual(t, res.ToolCalls, 1, "ToolCalls should reflect the tool dispatch")
}

func TestExtractActions_ParsesSuccessEnvelope(t *testing.T) {
	// Build a session with a snapshot of 2 entries (the inherited
	// prefix), then 2 tool-result entries (the reviewer's actions).
	sess := session.NewSession("main", "review-main")
	sess.Append(session.UserMessageEntry("user prompt"))
	sess.Append(session.AssistantMessageEntry("assistant reply"))
	inheritedCount := len(sess.View())

	sess.Append(session.ToolResultEntry("call-1", `{"success":true,"message":"saved memory: user prefers terse","target":"memory","id":"mem_x"}`, "", nil))
	sess.Append(session.ToolResultEntry("call-2", `{"success":true,"message":"created skill: my-flow","target":"skill","name":"my-flow"}`, "", nil))

	actions := extractActions(sess, inheritedCount)
	require.Len(t, actions, 2)
	assert.Equal(t, "saved memory: user prefers terse", actions[0])
	assert.Equal(t, "created skill: my-flow", actions[1])
}

func TestExtractActions_SkipsFailures(t *testing.T) {
	sess := session.NewSession("main", "review-main")
	inheritedCount := 0

	sess.Append(session.ToolResultEntry("call-1", `{"success":true,"message":"ok","target":"memory","id":"x"}`, "", nil))
	sess.Append(session.ToolResultEntry("call-2", `{"success":false,"error":"some error","target":"memory"}`, "", nil))
	sess.Append(session.ToolResultEntry("call-3", `{"success":true,"message":"another ok","target":"skill","name":"y"}`, "", nil))

	actions := extractActions(sess, inheritedCount)
	require.Len(t, actions, 2, "only successful results should appear")
	assert.Equal(t, "ok", actions[0])
	assert.Equal(t, "another ok", actions[1])
}

func TestExtractActions_SkipsNonEnvelopeOutputs(t *testing.T) {
	// Tools that don't follow the convention (e.g. return raw JSON
	// arrays for list, or plain text) should be silently skipped.
	sess := session.NewSession("main", "review-main")
	inheritedCount := 0

	sess.Append(session.ToolResultEntry("call-1", `[]`, "", nil))                                           // list output (raw array)
	sess.Append(session.ToolResultEntry("call-2", `{"success":true,"message":"ok","target":"x"}`, "", nil))
	sess.Append(session.ToolResultEntry("call-3", `not json at all`, "", nil))

	actions := extractActions(sess, inheritedCount)
	require.Len(t, actions, 1)
	assert.Equal(t, "ok", actions[0])
}

func TestExtractActions_OnlyAfterInheritedPrefix(t *testing.T) {
	// Tool-result entries from BEFORE inheritedCount must NOT be
	// extracted — those came from the parent's session, not the
	// reviewer's actions.
	sess := session.NewSession("main", "review-main")
	sess.Append(session.ToolResultEntry("inherit-1", `{"success":true,"message":"OLD action","target":"memory"}`, "", nil))
	sess.Append(session.UserMessageEntry("review prompt"))
	inheritedCount := len(sess.View())

	sess.Append(session.ToolResultEntry("review-1", `{"success":true,"message":"NEW action","target":"memory"}`, "", nil))

	actions := extractActions(sess, inheritedCount)
	require.Len(t, actions, 1, "only NEW actions appended after inheritedCount should appear")
	assert.Equal(t, "NEW action", actions[0])
}

func TestReview_SetsOriginToReviewOnContext(t *testing.T) {
	// Verify by registering a fake tool that captures the context's
	// OriginKey values and then asserting on them.

	parent := newReviewTestParent(t)
	parent.LLM = &statefulReviewMock{
		responses: [][]llm.ChatEvent{
			// First call: tool call to capture_origin.
			{
				{Type: llm.EventToolCallStart, ToolCall: &llm.ToolCall{ID: "tc_1", Name: "capture_origin"}},
				{Type: llm.EventToolCallDone, ToolCall: &llm.ToolCall{ID: "tc_1", Name: "capture_origin", Input: json.RawMessage(`{}`)}},
				{Type: llm.EventDone},
			},
			// Second call: finish with text.
			{
				{Type: llm.EventTextDelta, Text: "done"},
				{Type: llm.EventDone},
			},
		},
	}

	captured := make(chan struct {
		mem string
		ski string
	}, 1)
	reg := tool.NewRegistry()
	reg.Register(&originCapturingTool{captured: captured})

	res := Review(context.Background(), parent, ReviewSpec{
		Prompt: "x",
		Tools:  reg,
	})
	assert.NoError(t, res.Err)

	select {
	case got := <-captured:
		assert.Equal(t, "review", got.mem, `memory.OriginKey should be "review"`)
		assert.Equal(t, "review", got.ski, `skills.OriginKey should be "review"`)
	case <-time.After(2 * time.Second):
		t.Fatalf("originCapturingTool was never called")
	}
}

// originCapturingTool reads both memory.OriginKey and skills.OriginKey
// from the context and sends them to a channel.
type originCapturingTool struct {
	captured chan struct {
		mem string
		ski string
	}
}

func (o *originCapturingTool) Name() string                             { return "capture_origin" }
func (o *originCapturingTool) Description() string                      { return "test" }
func (o *originCapturingTool) Parameters() json.RawMessage              { return json.RawMessage(`{"type":"object"}`) }
func (o *originCapturingTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }
func (o *originCapturingTool) Execute(ctx context.Context, _ json.RawMessage) (tool.ToolResult, error) {
	mem, _ := ctx.Value(memory.OriginKey).(string)
	ski, _ := ctx.Value(skills.OriginKey).(string)
	o.captured <- struct {
		mem string
		ski string
	}{mem, ski}
	return tool.ToolResult{Output: `{"success":true,"message":"captured","target":"test"}`}, nil
}

func TestBuildReviewerRuntime_SpecOverridesParent(t *testing.T) {
	// Verify that spec values WIN over parent values when set.
	// Inheritance fallback is covered by the InheritsParent* tests above;
	// this test covers the symmetric "override" path so future refactors
	// can't silently swap precedence.

	parent := newReviewTestParent(t)
	parent.Memory = &fakeMemoryProvider{index: "parent-mem"}
	parent.Skills = &fakeSkillProvider{index: "parent-skills"}
	parent.ContextWindow = 100000

	overrideMem := &fakeMemoryProvider{index: "override-mem"}
	overrideSkills := &fakeSkillProvider{index: "override-skills"}
	overrideLLM := &llmtest.Stub{Text: "override"}

	rt, err := buildReviewerRuntime(parent, ReviewSpec{
		Prompt:        "x",
		Tools:         tool.NewRegistry(),
		Provider:      overrideLLM,
		Model:         "anthropic/claude-opus-4-7",
		ContextWindow: 200000,
		Memory:        overrideMem,
		Skills:        overrideSkills,
		MaxTurns:      32,
		SystemPrompt:  "I AM THE OVERRIDE PROMPT",
	})
	require.NoError(t, err)

	assert.Same(t, overrideLLM, rt.LLM, "spec.Provider should override parent.LLM")
	assert.Equal(t, "claude-opus-4-7", rt.Model, "spec.Model should override parent's recomposed model")
	assert.Equal(t, 200000, rt.ContextWindow, "spec.ContextWindow should override parent.ContextWindow")
	assert.NotSame(t, parent.Memory, rt.Memory, "spec.Memory should override parent.Memory")
	assert.NotSame(t, parent.Skills, rt.Skills, "spec.Skills should override parent.Skills")
	assert.Equal(t, 32, rt.MaxTurns, "spec.MaxTurns should override default 16")
	assert.Equal(t, "I AM THE OVERRIDE PROMPT", rt.SystemPrompt, "spec.SystemPrompt should be set on the reviewer")
}
