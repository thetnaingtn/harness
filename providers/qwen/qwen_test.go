package qwen

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/sausheong/harness/llm"
)

func TestQwenResolveSystemPromptPrefersParts(t *testing.T) {
	got := qwenResolveSystemPrompt(llm.ChatRequest{
		SystemPrompt:      "legacy",
		SystemPromptParts: []llm.SystemPromptPart{{Text: "new-a"}, {Text: "new-b"}},
	})
	require.Equal(t, "new-a\nnew-b", got)
}

func TestQwenResolveSystemPromptFallback(t *testing.T) {
	got := qwenResolveSystemPrompt(llm.ChatRequest{SystemPrompt: "legacy"})
	require.Equal(t, "legacy", got)
}

func TestQwenResolveSystemPromptEmpty(t *testing.T) {
	got := qwenResolveSystemPrompt(llm.ChatRequest{})
	require.Equal(t, "", got)
}
