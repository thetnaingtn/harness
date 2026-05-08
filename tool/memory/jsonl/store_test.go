package jsonl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sausheong/harness/tool/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_SaveAssignsID(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	e, err := s.Save(context.Background(), memory.Entry{Content: "user prefers concise responses"})
	require.NoError(t, err)
	assert.NotEmpty(t, e.ID)
	assert.True(t, strings.HasPrefix(e.ID, "mem_"), "id should start with mem_, got %q", e.ID)
	assert.Equal(t, "user prefers concise responses", e.Content)
	assert.False(t, e.CreatedAt.IsZero())
	assert.Equal(t, e.CreatedAt, e.UpdatedAt)
}

func TestStore_GetReturnsSavedEntry(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	saved, err := s.Save(context.Background(), memory.Entry{Content: "hello"})
	require.NoError(t, err)

	got, ok, err := s.Get(context.Background(), saved.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, saved.ID, got.ID)
	assert.Equal(t, "hello", got.Content)
}

func TestStore_GetUnknownReturnsFalseNoError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	got, ok, err := s.Get(context.Background(), "mem_does_not_exist")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, memory.Entry{}, got)
}

func TestStore_GetOnEmptyFileNoError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))
	// File doesn't exist; Get must not error.
	_, ok, err := s.Get(context.Background(), "mem_anything")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStore_ListReturnsAllInCreationOrder(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	a, _ := s.Save(context.Background(), memory.Entry{Content: "first"})
	b, _ := s.Save(context.Background(), memory.Entry{Content: "second"})
	c, _ := s.Save(context.Background(), memory.Entry{Content: "third"})

	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, a.ID, all[0].ID)
	assert.Equal(t, b.ID, all[1].ID)
	assert.Equal(t, c.ID, all[2].ID)
}

func TestStore_ListFiltersByTag(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	_, _ = s.Save(context.Background(), memory.Entry{Content: "general", Tags: []string{"misc"}})
	_, _ = s.Save(context.Background(), memory.Entry{Content: "user pref A", Tags: []string{"style", "pref"}})
	_, _ = s.Save(context.Background(), memory.Entry{Content: "user pref B", Tags: []string{"pref"}})

	pref, err := s.List(context.Background(), "pref")
	require.NoError(t, err)
	require.Len(t, pref, 2)
	for _, e := range pref {
		assert.Contains(t, e.Tags, "pref")
	}
}

func TestStore_ListEmptyFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))
	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestStore_UpdateRetiresOldIDAndAssignsNew(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	original, err := s.Save(context.Background(), memory.Entry{Content: "v1"})
	require.NoError(t, err)

	updated, err := s.Update(context.Background(), original.ID, "v2")
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, updated.ID, "Update must assign a fresh ID")
	assert.Equal(t, "v2", updated.Content)

	// Old id is gone.
	_, ok, err := s.Get(context.Background(), original.ID)
	require.NoError(t, err)
	assert.False(t, ok, "old id must be unreachable after Update")

	// New id is reachable.
	got, ok, err := s.Get(context.Background(), updated.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v2", got.Content)

	// List shows exactly one entry.
	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, updated.ID, all[0].ID)
}

func TestStore_UpdateChainCollapses(t *testing.T) {
	// v1 → v2 → v3. After all writes, only v3 should be visible.
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	v1, _ := s.Save(context.Background(), memory.Entry{Content: "v1"})
	v2, _ := s.Update(context.Background(), v1.ID, "v2")
	v3, _ := s.Update(context.Background(), v2.ID, "v3")

	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, v3.ID, all[0].ID)
	assert.Equal(t, "v3", all[0].Content)
}

func TestStore_UpdateUnknownReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	_, err := s.Update(context.Background(), "mem_does_not_exist", "v2")
	require.ErrorIs(t, err, memory.ErrNotFound)
}

func TestStore_UpdatePreservesOriginalCreatedAt(t *testing.T) {
	// Update should preserve CreatedAt from the original entry; UpdatedAt
	// should reflect the time of the update.
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	original, err := s.Save(context.Background(), memory.Entry{Content: "v1"})
	require.NoError(t, err)

	// Sleep a measurable amount so timestamps diverge.
	time.Sleep(10 * time.Millisecond)

	updated, err := s.Update(context.Background(), original.ID, "v2")
	require.NoError(t, err)

	assert.Equal(t, original.CreatedAt.UnixNano(), updated.CreatedAt.UnixNano(),
		"CreatedAt must be preserved from the original entry across Update")
	assert.True(t, updated.UpdatedAt.After(original.CreatedAt),
		"UpdatedAt must be later than original CreatedAt; got UpdatedAt=%v CreatedAt=%v",
		updated.UpdatedAt, original.CreatedAt)

	// Same invariant must hold after a fresh projection (List re-reads from disk).
	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, original.CreatedAt.UnixNano(), all[0].CreatedAt.UnixNano(),
		"CreatedAt must be preserved across reload")
	assert.True(t, all[0].UpdatedAt.After(all[0].CreatedAt),
		"UpdatedAt must be after CreatedAt across reload")
}

func TestStore_UpdateChainPreservesOriginalCreatedAt(t *testing.T) {
	// v1 → v2 → v3. v3's CreatedAt must equal v1's CreatedAt.
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	v1, _ := s.Save(context.Background(), memory.Entry{Content: "v1"})
	time.Sleep(5 * time.Millisecond)
	v2, _ := s.Update(context.Background(), v1.ID, "v2")
	time.Sleep(5 * time.Millisecond)
	v3, _ := s.Update(context.Background(), v2.ID, "v3")

	assert.Equal(t, v1.CreatedAt.UnixNano(), v3.CreatedAt.UnixNano(),
		"CreatedAt must propagate through the entire supersedes chain")
	assert.True(t, v3.UpdatedAt.After(v1.CreatedAt),
		"v3 UpdatedAt should be later than v1 CreatedAt")
}

func TestStore_RemoveDropsEntryFromList(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	a, _ := s.Save(context.Background(), memory.Entry{Content: "keep"})
	b, _ := s.Save(context.Background(), memory.Entry{Content: "drop"})

	require.NoError(t, s.Remove(context.Background(), b.ID))

	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, a.ID, all[0].ID)

	_, ok, err := s.Get(context.Background(), b.ID)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStore_RemoveUnknownIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))
	require.NoError(t, s.Remove(context.Background(), "mem_does_not_exist"))
	require.NoError(t, s.Remove(context.Background(), "mem_does_not_exist"))
}

func TestStore_RemoveAfterUpdateAffectsNewID(t *testing.T) {
	// After Update, the OLD id is already gone; only the NEW id remains
	// removable. Removing the OLD id is a no-op.
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	v1, _ := s.Save(context.Background(), memory.Entry{Content: "v1"})
	v2, _ := s.Update(context.Background(), v1.ID, "v2")

	require.NoError(t, s.Remove(context.Background(), v1.ID)) // no-op
	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 1) // v2 still present

	require.NoError(t, s.Remove(context.Background(), v2.ID))
	all, err = s.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestStore_TornLineIsSkipped(t *testing.T) {
	// Simulate a crash mid-write: append a complete entry, then a
	// corrupted half-line, then another complete entry. Projection
	// must keep both completes and silently drop the torn line.
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	s := NewStore(path)

	a, err := s.Save(context.Background(), memory.Entry{Content: "before"})
	require.NoError(t, err)

	// Manually inject a torn line.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"op":"create","id":"mem_torn","content":"this line is incomplete`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, appendNewline(path))

	b, err := s.Save(context.Background(), memory.Entry{Content: "after"})
	require.NoError(t, err)

	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 2, "torn line must be skipped, before+after must remain")
	ids := []string{all[0].ID, all[1].ID}
	assert.Contains(t, ids, a.ID)
	assert.Contains(t, ids, b.ID)
}

func appendNewline(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte{'\n'})
	return err
}

func TestStore_CompactRemovesTombstonesAndSupersededEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")
	s := NewStore(path)

	a, _ := s.Save(context.Background(), memory.Entry{Content: "keep-1"})
	b, _ := s.Save(context.Background(), memory.Entry{Content: "drop"})
	c, _ := s.Save(context.Background(), memory.Entry{Content: "keep-2"})

	// b → tombstoned
	require.NoError(t, s.Remove(context.Background(), b.ID))
	// c → updated to c2; c retired
	c2, _ := s.Update(context.Background(), c.ID, "keep-2-updated")

	beforeStat, err := os.Stat(path)
	require.NoError(t, err)
	beforeSize := beforeStat.Size()

	require.NoError(t, s.Compact(context.Background()))

	afterStat, err := os.Stat(path)
	require.NoError(t, err)
	assert.Less(t, afterStat.Size(), beforeSize, "compaction should shrink the file")

	// Live state must be unchanged.
	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, all, 2)
	ids := []string{all[0].ID, all[1].ID}
	assert.Contains(t, ids, a.ID)
	assert.Contains(t, ids, c2.ID)
	assert.NotContains(t, ids, b.ID)
	assert.NotContains(t, ids, c.ID)
}

func TestStore_CompactOnEmptyStoreNoError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))
	require.NoError(t, s.Compact(context.Background()))
}

func TestStore_ConcurrentSavesDoNotCorruptFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			_, err := s.Save(context.Background(), memory.Entry{
				Content: fmt.Sprintf("entry %d", i),
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("save error: %v", e)
	}

	all, err := s.List(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, all, N, "all 50 concurrent saves must be readable")
}

func TestStore_FormatIndexEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	provider := s.AsMemoryProvider()
	assert.Empty(t, provider.FormatIndex())
}

func TestStore_FormatIndexNonEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	_, _ = s.Save(context.Background(), memory.Entry{Content: "User prefers concise responses."})
	_, _ = s.Save(context.Background(), memory.Entry{Content: "Project uses Go 1.25."})

	provider := s.AsMemoryProvider()
	idx := provider.FormatIndex()
	assert.Contains(t, idx, "## Memory")
	assert.Contains(t, idx, "User prefers concise responses.")
	assert.Contains(t, idx, "Project uses Go 1.25.")
}

func TestStore_AsMemoryProviderGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "memory.jsonl"))

	saved, _ := s.Save(context.Background(), memory.Entry{Content: "hello"})

	provider := s.AsMemoryProvider()
	body, ok := provider.Get(saved.ID)
	assert.True(t, ok)
	assert.Equal(t, "hello", body)

	_, ok = provider.Get("mem_does_not_exist")
	assert.False(t, ok)
}
