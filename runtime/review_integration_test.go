package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/harness/tool/memory/jsonl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReview_Integration_MemorySaveRoundTrip wires:
//   - jsonl.Store (real disk-backed memory, t.TempDir)
//   - memory.MemoryTool over the store
//   - Review with the tool registry restricted to memory only
//   - A fake LLM (statefulReviewMock) that emits a single tool-call to memory.save
//
// Expected: after Review completes, the memory store has one entry with
// origin="review", visible via Get and FormatIndex.
func TestReview_Integration_MemorySaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	memStore := jsonl.NewStore(filepath.Join(dir, "memory.jsonl"))

	reviewTools := tool.NewRegistry()
	reviewTools.Register(&memory.MemoryTool{Store: memStore})

	// Fake LLM: respond to the review prompt by calling memory.save once,
	// then finish.
	mock := &statefulReviewMock{
		responses: [][]llm.ChatEvent{
			// First call: emit a tool call to memory with action=save.
			{
				{Type: llm.EventToolCallStart, ToolCall: &llm.ToolCall{
					ID: "tc_1", Name: "memory",
				}},
				{Type: llm.EventToolCallDone, ToolCall: &llm.ToolCall{
					ID:    "tc_1",
					Name:  "memory",
					Input: []byte(`{"action":"save","content":"user prefers terse explanations"}`),
				}},
				{Type: llm.EventDone},
			},
			// Second call: finish with text.
			{
				{Type: llm.EventTextDelta, Text: "Saved."},
				{Type: llm.EventDone},
			},
		},
	}

	parent := &Runtime{
		AgentID:   "main",
		AgentName: "Main",
		Provider:  "anthropic",
		Model:     "claude-haiku-4-5",
		Workspace: dir,
		Session:   session.NewSession("main", "main"),
		LLM:       mock,
	}
	// Pre-seed parent's session with a sentinel exchange so the reviewer
	// has something to read.
	parent.Session.Append(session.UserMessageEntry("please use terse explanations"))
	parent.Session.Append(session.AssistantMessageEntry("Got it."))

	res := Review(context.Background(), parent, ReviewSpec{
		Prompt: "review and save anything worth remembering",
		Tools:  reviewTools,
	})
	require.NoError(t, res.Err, "Review setup must succeed")
	require.Len(t, res.Actions, 1, "exactly one save action expected")
	assert.Contains(t, res.Actions[0], "saved memory")

	// The store has the entry.
	all, err := memStore.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "user prefers terse explanations", all[0].Content)
	assert.Equal(t, "review", all[0].Origin, `Origin must be set to "review" by the OriginKey context`)

	// The provider adapter sees it too — proves the system-prompt path
	// would pick it up on the next Run.
	provider := memStore.AsMemoryProvider()
	idx := provider.FormatIndex()
	assert.Contains(t, idx, "user prefers terse explanations")
}
