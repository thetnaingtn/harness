// Package runtime — Review primitive.
//
// runtime.Review runs a one-shot reviewer Runtime against a finished
// parent session, designed to be called from LifecycleHooks.OnStop in
// a goroutine. The reviewer sees a snapshot of the parent's
// conversation but writes durable memory/skills into the SHARED stores
// the parent reads from — that's how an agent built on harness curates
// its own memory and skill library across sessions.
//
// Typical wiring (10 lines from OnStop):
//
//	spec.Loop.Hooks.OnStop = func(ctx context.Context, reason string) {
//	    if reason != "completed" { return }
//	    go func() {
//	        runtime.Review(context.Background(), rt, runtime.ReviewSpec{
//	            Prompt: runtime.ReviewPromptDefault,
//	            Tools:  reviewTools,
//	        })
//	    }()
//	}
//
// Note: pass context.Background() — the parent's ctx is canceled by
// the time OnStop fires.
//
// Concurrency: if your OnStop loop fires Review concurrently (e.g.,
// back-to-back turns where reviewer N is still running when turn N+1
// completes), ensure your MemoryStore/SkillStore are concurrency-safe.
// The bundled tool/memory/jsonl and tool/skills/disk implementations
// are; custom backends must guarantee safe concurrent writes.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/harness/tool/skills"
)

// ReviewSpec describes one reviewer pass over the parent Runtime's
// just-finished session. Only Prompt and Tools are required.
type ReviewSpec struct {
	// Prompt is the user-message-shaped review instruction handed to
	// the reviewer LLM. Use ReviewPromptDefault or ReviewPromptVerbose
	// from review_prompts.go, or write your own.
	Prompt string
	// Tools is the restricted toolset the reviewer can call. Typically
	// just write-only memory + skill tools so the reviewer can't read
	// files or execute shell commands. Required.
	Tools tool.Executor

	// MaxTurns caps the reviewer's tool-use loop, counted independently
	// of the parent's MaxTurns. 0 → default 16.
	MaxTurns int
	// Timeout is the hard envelope for the reviewer's Run. The reviewer
	// is canceled when exceeded; partial Actions are returned. 0 →
	// default 60 seconds.
	Timeout time.Duration

	// Provider, when non-nil, routes the reviewer to a (typically
	// cheaper) auxiliary LLM. nil → inherit parent.LLM.
	Provider llm.LLMProvider
	// Model overrides the parent's model id. "" → inherit parent.Model.
	Model string
	// ContextWindow overrides the parent's context window. 0 → inherit.
	ContextWindow int
	// SystemPrompt overrides the reviewer's static system prompt. "" →
	// the reviewer gets a minimal default identity composed from its
	// tool names.
	SystemPrompt string

	// Permission, when non-nil, gates the reviewer's tool calls.
	// nil → allow-all (the reviewer's tool registry is already
	// restricted by spec.Tools).
	Permission tool.PermissionChecker
	// Memory, when non-nil, overrides the parent's MemoryProvider.
	// nil → inherit (writes share the parent's memory store).
	Memory MemoryProvider
	// Skills, when non-nil, overrides the parent's SkillProvider.
	// nil → inherit (writes share the parent's skill store).
	Skills SkillProvider

	// OnEvent, when non-nil, receives a copy of every reviewer event
	// as it arrives. nil → events are drained silently. Run on the
	// reviewer goroutine; keep callbacks fast.
	OnEvent func(AgentEvent)
}

// ReviewResult summarizes one Review pass.
type ReviewResult struct {
	// Actions is the list of human-readable one-liners extracted from
	// the reviewer's successful tool calls (the "message" field of the
	// canonical {"success": true, "message": "...", "target": "..."}
	// JSON envelope).
	Actions []string
	// ToolCalls is the count of tool_call entries the reviewer made
	// (regardless of success).
	ToolCalls int
	// Turns is the count of LLM turns the reviewer used.
	Turns int
	// Duration is the wall time from the start of Review to its return
	// (including snapshot + Run + extraction).
	Duration time.Duration
	// Err is non-nil for setup failures (recursion guard, nil Tools,
	// build errors). Mid-Run failures (LLM errors, tool errors) are
	// counted in Turns/ToolCalls but do not populate Err — partial
	// Actions are returned.
	Err error
}

// ErrReviewRecursion is returned (via ReviewResult.Err) when Review is
// called on a Runtime whose AgentID is the reserved "__review__"
// sentinel — i.e., the reviewer would itself spawn a reviewer.
var ErrReviewRecursion = errors.New("runtime: review recursion (parent is itself a reviewer)")

// reviewerAgentID is the reserved AgentID assigned to every reviewer
// Runtime. Review checks parent.AgentID against this value and refuses
// to recurse.
const reviewerAgentID = "__review__"

// Review runs a one-shot reviewer Runtime against parent's just-
// finished session. See package doc for the typical OnStop wiring.
//
// Required spec fields: Prompt, Tools. All other fields have sensible
// defaults (see ReviewSpec doc-comments).
//
// Returns ReviewResult with non-nil Err for setup failures (nil parent,
// recursion guard, nil Tools, empty Prompt, build errors). Mid-Run
// failures populate Turns/ToolCalls but leave Err nil.
//
// Cancelation: pass context.Background() when calling from OnStop —
// the parent's ctx is canceled by the time OnStop fires.
func Review(ctx context.Context, parent *Runtime, spec ReviewSpec) ReviewResult {
	start := time.Now()
	if parent == nil {
		return ReviewResult{Err: errors.New("runtime.Review: parent Runtime is nil"), Duration: time.Since(start)}
	}
	if parent.AgentID == reviewerAgentID {
		return ReviewResult{Err: ErrReviewRecursion, Duration: time.Since(start)}
	}
	if spec.Tools == nil {
		return ReviewResult{Err: errors.New("runtime.Review: spec.Tools is required"), Duration: time.Since(start)}
	}
	if spec.Prompt == "" {
		return ReviewResult{Err: errors.New("runtime.Review: spec.Prompt is required"), Duration: time.Since(start)}
	}

	rt, err := buildReviewerRuntime(parent, spec)
	if err != nil {
		return ReviewResult{Err: err, Duration: time.Since(start)}
	}
	defer rt.Close()

	// Capture the inherited prefix length BEFORE the reviewer runs so
	// extractActions can skip parent-session entries and only extract
	// tool-result envelopes appended by the reviewer itself.
	inheritedCount := len(rt.Session.View())

	// Apply hard timeout. If spec.Timeout is 0, default to 60s.
	timeout := spec.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Provenance: tag every memory/skills write the reviewer makes
	// with origin "review". Both keys are independent (per-package
	// design); set both so whichever tool the reviewer calls picks up
	// the right value without coordination.
	runCtx = context.WithValue(runCtx, memory.OriginKey, "review")
	runCtx = context.WithValue(runCtx, skills.OriginKey, "review")

	events, err := rt.Run(runCtx, spec.Prompt, nil)
	if err != nil {
		return ReviewResult{Err: err, Duration: time.Since(start)}
	}

	// Drain events. Forward to OnEvent if set; count Turns and ToolCalls.
	var turns, toolCalls int
	for ev := range events {
		if spec.OnEvent != nil {
			spec.OnEvent(ev)
		}
		switch ev.Type {
		case EventToolCallStart:
			toolCalls++
		case EventDone:
			turns++
		}
	}

	actions := extractActions(rt.Session, inheritedCount)

	return ReviewResult{
		Actions:   actions,
		Turns:     turns,
		ToolCalls: toolCalls,
		Duration:  time.Since(start),
	}
}

// extractActions walks sess.View() starting at startIdx and returns
// the Message field of every tool_result entry whose Output parses as
// the canonical action-result envelope with Success=true.
//
// Convention is over schema: tools that don't emit the envelope
// (e.g. list returns a raw array, get returns the entry struct, edit
// tools return plain text) are silently skipped.
//
// Note: SessionEntry stores the per-type payload as a json.RawMessage
// in the Data field, so we first unmarshal Data into ToolResultData to
// recover Output, then unmarshal Output into the action envelope.
func extractActions(sess *session.Session, startIdx int) []string {
	view := sess.View()
	if startIdx >= len(view) {
		return nil
	}
	var actions []string
	for _, e := range view[startIdx:] {
		if e.Type != session.EntryTypeToolResult {
			continue
		}
		var tr session.ToolResultData
		if err := json.Unmarshal(e.Data, &tr); err != nil {
			continue
		}
		var env struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(tr.Output), &env); err != nil {
			continue
		}
		if !env.Success || env.Message == "" {
			continue
		}
		actions = append(actions, env.Message)
	}
	return actions
}

// buildReviewerRuntime constructs a Runtime configured for a one-shot
// review pass over parent's just-finished session.
//
// Inheritance defaults:
//   - LLM: spec.Provider, falling back to parent.LLM
//   - Model: spec.Model, falling back to parent.Provider+"/"+parent.Model
//   - ContextWindow: spec.ContextWindow, falling back to parent.ContextWindow
//   - Workspace: parent.Workspace (for trace/spill paths)
//   - Memory: spec.Memory, falling back to parent.Memory (SHARED, not snapshotted)
//   - Skills: spec.Skills, falling back to parent.Skills (SHARED)
//   - Permission: spec.Permission (NOT inherited from parent — defaults to nil)
//   - SystemPrompt: spec.SystemPrompt (override) or empty (default identity built from tool names)
//   - MaxTurns: spec.MaxTurns or 16
//
// Always-set on reviewer:
//   - AgentID = "__review__" (recursion guard)
//   - IngestSource = "review" (KG ingest skips)
//   - Hooks zeroed (no OnStop reentry, no observation noise)
//   - KG nil (reviewer doesn't recall or ingest)
//   - Compaction nil (short-lived; MaxTurns 16 keeps context small)
func buildReviewerRuntime(parent *Runtime, spec ReviewSpec) (*Runtime, error) {
	provider := spec.Provider
	if provider == nil {
		provider = parent.LLM
	}
	model := spec.Model
	if model == "" {
		// Re-compose parent.Provider/parent.Model since runtime stores
		// them split (after ParseProviderModel).
		if parent.Provider != "" && parent.Model != "" {
			model = parent.Provider + "/" + parent.Model
		} else {
			model = parent.Model // best-effort fallback when parent has no provider
		}
	}
	ctxWindow := spec.ContextWindow
	if ctxWindow == 0 {
		ctxWindow = parent.ContextWindow
	}
	mem := spec.Memory
	if mem == nil {
		mem = parent.Memory
	}
	skillProv := spec.Skills
	if skillProv == nil {
		skillProv = parent.Skills
	}
	maxTurns := spec.MaxTurns
	if maxTurns == 0 {
		maxTurns = 16
	}

	deps := RuntimeDeps{
		Skills:     skillProv,
		Memory:     mem,
		Permission: spec.Permission, // defaults to nil = allow-all
		// KGFn nil — reviewer never recalls or ingests
		// AgentLoop zeroed — Hooks left nil so no OnStop reentry
	}
	inputs := RuntimeInputs{
		Provider:     provider,
		Tools:        spec.Tools,
		Session:      snapshotSession(parent.Session),
		Compaction:   nil, // reviewer is short-lived
		IngestSource: "review",
	}
	agentSpec := AgentSpec{
		ID:            reviewerAgentID,
		Name:          "Review",
		Model:         model,
		Workspace:     parent.Workspace,
		SystemPrompt:  spec.SystemPrompt,
		MaxTurns:      maxTurns,
		ContextWindow: ctxWindow,
	}
	rt, err := BuildRuntime(deps, inputs, agentSpec)
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// snapshotSession returns a fresh *session.Session that contains a
// copy of parent's current View. The clone preserves AgentID (so the
// reviewer sees the same agent identity in entries) but rewrites Key
// to "review-" + parent.Key — this ensures the reviewer's compaction
// locks don't contend with the parent's (locks are keyed on
// AgentID/Key) and that the JSONL store (if attached) writes to a
// separate file.
//
// The clone is in-memory only; SetStore is NOT called on it.
//
// Mirror of inheritParentHistory in subagent.go: the FIRST inherited
// entry gets its ParentID cleared so the fresh session's empty leaf
// doesn't leave a dangling pointer; subsequent entries chain naturally
// as Append re-stitches them.
func snapshotSession(parent *session.Session) *session.Session {
	clone := session.NewSession(parent.AgentID, "review-"+parent.Key)
	view := parent.View()
	for i, e := range view {
		if i == 0 {
			e.ParentID = ""
		}
		clone.Append(e)
	}
	return clone
}
