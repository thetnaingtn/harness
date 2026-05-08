package memory_test

// Separate _test package to verify the public surface wires together
// without internal fakes.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/harness/tool/memory/jsonl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEndToEnd_SaveListUpdateRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := jsonl.NewStore(filepath.Join(dir, "memory.jsonl"))
	tool := &memory.MemoryTool{Store: store}

	// Save two entries.
	for _, content := range []string{"first preference", "second preference"} {
		in, _ := json.Marshal(map[string]any{
			"action":  "save",
			"content": content,
			"tags":    []string{"pref"},
		})
		res, err := tool.Execute(context.Background(), in)
		require.NoError(t, err)
		assert.Empty(t, res.Error)
	}

	// List both back.
	in, _ := json.Marshal(map[string]any{"action": "list"})
	res, err := tool.Execute(context.Background(), in)
	require.NoError(t, err)
	var entries []memory.Entry
	require.NoError(t, json.Unmarshal([]byte(res.Output), &entries))
	require.Len(t, entries, 2)

	// Update the first.
	firstID := entries[0].ID
	in, _ = json.Marshal(map[string]any{
		"action":  "update",
		"id":      firstID,
		"content": "first preference (revised)",
	})
	res, err = tool.Execute(context.Background(), in)
	require.NoError(t, err)
	var ar struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Output), &ar))
	assert.True(t, ar.Success)
	newID := ar.ID
	assert.NotEqual(t, firstID, newID)

	// Old id is gone via the provider adapter (the system-prompt path).
	provider := store.AsMemoryProvider()
	_, ok := provider.Get(firstID)
	assert.False(t, ok, "old id must be unreachable through MemoryProvider")
	body, ok := provider.Get(newID)
	assert.True(t, ok)
	assert.Equal(t, "first preference (revised)", body)

	// FormatIndex includes both live entries.
	idx := provider.FormatIndex()
	assert.Contains(t, idx, "first preference (revised)")
	assert.Contains(t, idx, "second preference")
	assert.NotContains(t, idx, "first preference\n", "old version must not appear")

	// Remove the second.
	secondID := entries[1].ID
	in, _ = json.Marshal(map[string]any{"action": "remove", "id": secondID})
	_, err = tool.Execute(context.Background(), in)
	require.NoError(t, err)

	in, _ = json.Marshal(map[string]any{"action": "list"})
	res, err = tool.Execute(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(res.Output), &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "first preference (revised)", entries[0].Content)
}

func TestEndToEnd_ProviderRoundTripAfterCompact(t *testing.T) {
	// Save 5, remove 2, update 1, compact, verify state preserved.
	dir := t.TempDir()
	store := jsonl.NewStore(filepath.Join(dir, "memory.jsonl"))
	memTool := &memory.MemoryTool{Store: store}

	ids := []string{}
	for range 5 {
		in, _ := json.Marshal(map[string]any{
			"action":  "save",
			"content": "entry",
		})
		res, _ := memTool.Execute(context.Background(), in)
		var ar struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(res.Output), &ar)
		ids = append(ids, ar.ID)
	}

	// Remove 2.
	for _, id := range ids[:2] {
		in, _ := json.Marshal(map[string]any{"action": "remove", "id": id})
		_, _ = memTool.Execute(context.Background(), in)
	}

	// Update 1.
	in, _ := json.Marshal(map[string]any{
		"action":  "update",
		"id":      ids[2],
		"content": "updated",
	})
	_, _ = memTool.Execute(context.Background(), in)

	require.NoError(t, store.Compact(context.Background()))

	// State preserved post-compact.
	all, err := store.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 3)
}
