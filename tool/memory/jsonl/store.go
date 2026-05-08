// Package jsonl is the default disk-backed MemoryStore implementation.
// It uses one append-only JSONL file with three line shapes:
//
//   - create:    {"op":"create", id, content, tags, origin, created_at}
//   - update:    {"op":"update", id, supersedes, content, origin, created_at, original_created_at}
//   - delete:    {"op":"delete", id, origin, created_at}
//
// Reads project the file by walking it once: latest write wins,
// tombstoned ids are dropped, superseded chains collapse so old IDs
// don't ghost.
//
// Crash safety rests on three properties:
//
//  1. O_APPEND writes are atomic for buffers ≤ PIPE_BUF (≥4KB on POSIX).
//     The MemoryTool caps content at 4KB so a single line never
//     interleaves with concurrent appenders.
//  2. fsync is called after each append.
//  3. Torn lines on crash are tolerated — the projection skips
//     unparseable lines.
//
// Cross-process safety: appendLine acquires flock(LOCK_EX); projectLocked
// acquires flock(LOCK_SH). The store assumes at most a few processes
// share one file. The locks are advisory POSIX flocks (BSD style); they
// do not prevent processes that don't call flock from corrupting the
// file. Do NOT point hundreds of processes at the same store. On
// Windows the locks are no-ops; single-process users are unaffected.
package jsonl

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sausheong/harness/tool/memory"
)

// Store is a JSONL-on-disk MemoryStore.
type Store struct {
	path string
	mu   sync.RWMutex
}

// NewStore returns a Store backed by path. The file is created lazily
// on first write; reads of a non-existent file return an empty result.
// The parent directory must exist.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// line is the on-disk row shape for all three op types. Optional fields
// stay omitted when zero so the file stays grep-friendly.
type line struct {
	Op         string   `json:"op"` // "create" | "update" | "delete"
	ID         string   `json:"id"`
	Content    string   `json:"content,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Origin     string   `json:"origin,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	// CreatedAt is the time THIS line was written (treated as the
	// entry's UpdatedAt during projection).
	CreatedAt time.Time `json:"created_at"`
	// OriginalCreatedAt is set on update lines to carry the original
	// entry's birth time forward through the supersedes chain. Omitted
	// on create and delete lines.
	OriginalCreatedAt time.Time `json:"original_created_at,omitzero"`
}

// generateID returns a sortable, collision-resistant id of the form
// mem_YYYY-MM-DD_<8-char-hex>. The hex tail makes same-day collisions
// astronomically unlikely (2^32 space) while keeping ids short.
func generateID(now time.Time) string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("mem_%s_%s", now.UTC().Format("2006-01-02"), hex.EncodeToString(buf[:]))
}

// appendLine writes one line to the data file with O_APPEND + fsync.
// Caller holds s.mu in write mode.
func (s *Store) appendLine(l line) error {
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	if err := lockExclusive(f); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	enc := json.NewEncoder(f) // adds trailing newline automatically
	if err := enc.Encode(l); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync: %w", err)
	}
	return nil
}

// Save implements memory.MemoryStore.
func (s *Store) Save(_ context.Context, e memory.Entry) (memory.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if e.ID == "" {
		e.ID = generateID(now)
	}
	e.CreatedAt = now
	e.UpdatedAt = now
	if err := s.appendLine(line{
		Op:        "create",
		ID:        e.ID,
		Content:   e.Content,
		Tags:      e.Tags,
		Origin:    e.Origin,
		CreatedAt: now,
	}); err != nil {
		return memory.Entry{}, err
	}
	return e, nil
}

// Update implements memory.MemoryStore. The new entry gets a fresh ID
// and supersedes the old one in projection. The original entry's
// CreatedAt is preserved on the new entry; UpdatedAt reflects the time
// of the update write. Tags and Origin are preserved from the original.
func (s *Store) Update(_ context.Context, id, content string) (memory.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, _, err := s.projectLocked()
	if err != nil {
		return memory.Entry{}, err
	}
	old, ok := entries[id]
	if !ok {
		return memory.Entry{}, memory.ErrNotFound
	}

	now := time.Now().UTC()
	newID := generateID(now)
	if err := s.appendLine(line{
		Op:                "update",
		ID:                newID,
		Supersedes:        id,
		Content:           content,
		Tags:              old.Tags,   // preserve tags
		Origin:            old.Origin, // preserve origin
		CreatedAt:         now,
		OriginalCreatedAt: old.CreatedAt,
	}); err != nil {
		return memory.Entry{}, err
	}

	return memory.Entry{
		ID:        newID,
		Content:   content,
		Tags:      old.Tags,
		Origin:    old.Origin,
		CreatedAt: old.CreatedAt, // preserved birth time
		UpdatedAt: now,
	}, nil
}

// Remove implements memory.MemoryStore. Idempotent: removing an unknown
// id returns nil. The store still appends a tombstone in that case to
// keep the operation auditable; projection treats the tombstone as
// authoritative regardless of whether a matching create exists.
func (s *Store) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	return s.appendLine(line{
		Op:        "delete",
		ID:        id,
		CreatedAt: now,
	})
}

// List implements memory.MemoryStore.
func (s *Store) List(_ context.Context, tag string) ([]memory.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, _, err := s.projectLocked()
	if err != nil {
		return nil, err
	}
	out := make([]memory.Entry, 0, len(entries))
	for _, e := range entries {
		if tag != "" && !slices.Contains(e.Tags, tag) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// projectLocked walks the file once and returns the live entry map +
// the set of tombstoned ids. Caller holds s.mu (any mode).
//
// Returns (empty maps, nil) when the file does not exist — a fresh store
// has no entries, not an error.
func (s *Store) projectLocked() (map[string]memory.Entry, map[string]struct{}, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]memory.Entry{}, map[string]struct{}{}, nil
		}
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	if err := lockShared(f); err != nil {
		return nil, nil, fmt.Errorf("lock: %w", err)
	}

	entries := map[string]memory.Entry{}
	deleted := map[string]struct{}{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1 MB max line
	for scanner.Scan() {
		var l line
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			// Torn / unparseable line — skip. Last entry on a crashed
			// process may be incomplete; we tolerate that.
			continue
		}
		switch l.Op {
		case "delete":
			delete(entries, l.ID)
			deleted[l.ID] = struct{}{}
		case "create", "update":
			if _, gone := deleted[l.ID]; gone {
				// Defensive: a later tombstone for this id has already
				// been seen. Out-of-order writes from concurrent
				// processes shouldn't happen with our flock, but skip
				// just in case.
				continue
			}
			if l.Supersedes != "" {
				delete(entries, l.Supersedes)
				// supersedes only retires the OLD id; the new id is
				// recorded below.
			}
			createdAt := l.CreatedAt
			if !l.OriginalCreatedAt.IsZero() {
				createdAt = l.OriginalCreatedAt
			}
			entries[l.ID] = memory.Entry{
				ID:        l.ID,
				Content:   l.Content,
				Tags:      l.Tags,
				Origin:    l.Origin,
				CreatedAt: createdAt,
				UpdatedAt: l.CreatedAt,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scan: %w", err)
	}
	return entries, deleted, nil
}

// Compact rewrites the data file with only live entries (no tombstones,
// no superseded creates). Atomic: writes to a temp file then renames
// over the data file. Caller is responsible for picking when to call
// (typically when len(file) > N × len(live entries)).
//
// Holds s.mu.Lock for the duration. Concurrent reads and writes to
// this Store are blocked until Compact returns.
//
// Compaction preserves each live entry's original CreatedAt: the
// rewritten file uses one create line per entry, with the entry's
// original birth time as the line's created_at. The supersedes-chain
// history is collapsed.
func (s *Store) Compact(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, _, err := s.projectLocked()
	if err != nil {
		return err
	}

	tmp := s.path + ".compacting"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	enc := json.NewEncoder(f)

	// Write in CreatedAt order so the compacted file remains
	// chronological — handy for grep/inspection.
	ordered := make([]memory.Entry, 0, len(entries))
	for _, e := range entries {
		ordered = append(ordered, e)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})

	for _, e := range ordered {
		if err := enc.Encode(line{
			Op:        "create",
			ID:        e.ID,
			Content:   e.Content,
			Tags:      e.Tags,
			Origin:    e.Origin,
			CreatedAt: e.CreatedAt,
		}); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encode: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	// Fsync the parent directory so the rename is durable across crashes.
	// On Linux, an unfsync'd directory metadata change can be rolled back
	// after a power loss even though the rename(2) syscall succeeded.
	// On Windows, opening a directory like this isn't supported; ignore
	// the error there since flock is also a no-op.
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Get implements memory.MemoryStore.
func (s *Store) Get(_ context.Context, id string) (memory.Entry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, _, err := s.projectLocked()
	if err != nil {
		return memory.Entry{}, false, err
	}
	e, ok := entries[id]
	return e, ok, nil
}

// MemoryProviderAdapter wraps a *Store with the read-only signature
// runtime.MemoryProvider expects (FormatIndex + Get(string)(string,bool)).
// Lets one *Store value serve both writes (via the MemoryStore interface)
// and reads (via runtime.MemoryProvider).
type MemoryProviderAdapter struct{ s *Store }

// AsMemoryProvider returns a wrapper that satisfies
// runtime.MemoryProvider — pass it as RuntimeInputs.Memory while still
// using the same *Store as the MemoryTool's backend.
func (s *Store) AsMemoryProvider() *MemoryProviderAdapter {
	return &MemoryProviderAdapter{s: s}
}

// FormatIndex satisfies runtime.MemoryProvider. Returns a markdown
// block for inclusion in the static system prompt; empty when the
// store has no live entries.
func (a *MemoryProviderAdapter) FormatIndex() string {
	entries, err := a.s.List(context.Background(), "")
	if err != nil || len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Memory\n\n")
	for _, e := range entries {
		b.WriteString("- ")
		b.WriteString(e.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// Get satisfies runtime.MemoryProvider. Returns the entry's content
// (not the full Entry struct) so the runtime interface stays simple.
func (a *MemoryProviderAdapter) Get(id string) (string, bool) {
	e, ok, err := a.s.Get(context.Background(), id)
	if err != nil || !ok {
		return "", false
	}
	return e.Content, true
}
