// Five concrete tools for the support agent. The runtime auto-registers
// a sixth (load_skill) because deps.Skills is set, so the agent can
// pull full KB article bodies on demand keyed by the slugs surfaced in
// the static prompt's Knowledge Base index.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sausheong/harness/tool"
)

func errResult(err error) tool.ToolResult { return tool.ToolResult{Error: err.Error()} }

func jsonOut(v any) tool.ToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return tool.ToolResult{Output: string(b)}
}

// =====================================================================
// kb_search — substring match across slugs + summaries + bodies
// =====================================================================

type KBSearchTool struct {
	KB *KBSkills
}

func (*KBSearchTool) Name() string { return "kb_search" }
func (*KBSearchTool) Description() string {
	return "Search the Acme knowledge base by free-text query. Returns " +
		"matching article slugs with one-line summaries and a snippet " +
		"showing where the query hit. Pair with load_skill(slug) to read " +
		"the full article body before quoting policy."
}
func (*KBSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Substring to match (case-insensitive). 2+ characters."
			}
		},
		"required": ["query"]
	}`)
}
func (*KBSearchTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *KBSearchTool) Execute(_ context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	if len(q) < 2 {
		return errResult(fmt.Errorf("query must be at least 2 characters")), nil
	}

	type hit struct {
		Slug    string `json:"slug"`
		Summary string `json:"summary"`
		Snippet string `json:"snippet,omitempty"`
	}
	var hits []hit
	for _, slug := range t.KB.Slugs() {
		summary, body, _ := t.KB.Article(slug)
		lowSlug, lowSum, lowBody := strings.ToLower(slug), strings.ToLower(summary), strings.ToLower(body)
		if !strings.Contains(lowSlug, q) && !strings.Contains(lowSum, q) && !strings.Contains(lowBody, q) {
			continue
		}
		// Build a snippet around the first body hit (if any) to give the
		// model enough context to decide whether to load_skill the
		// full article.
		snippet := ""
		if idx := strings.Index(lowBody, q); idx >= 0 {
			start := idx - 80
			if start < 0 {
				start = 0
			}
			end := idx + len(q) + 120
			if end > len(body) {
				end = len(body)
			}
			snippet = strings.TrimSpace(body[start:end])
			snippet = strings.ReplaceAll(snippet, "\n", " ")
			if start > 0 {
				snippet = "…" + snippet
			}
			if end < len(body) {
				snippet = snippet + "…"
			}
		}
		hits = append(hits, hit{slug, summary, snippet})
	}

	return jsonOut(map[string]any{
		"query":   in.Query,
		"matches": len(hits),
		"results": hits,
	}), nil
}

// =====================================================================
// tickets_get — fetch one ticket by ID
// =====================================================================

type TicketGetTool struct {
	Store *TicketStore
}

func (*TicketGetTool) Name() string { return "tickets_get" }
func (*TicketGetTool) Description() string {
	return "Fetch a support ticket by its ID (format 'T-####'). Returns the " +
		"full ticket including status, priority, customer ID, history, and " +
		"any internal notes. Read-only — never mutates."
}
func (*TicketGetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "Ticket ID, e.g. 'T-1002'."
			}
		},
		"required": ["id"]
	}`)
}
func (*TicketGetTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func (t *TicketGetTool) Execute(_ context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	if strings.TrimSpace(in.ID) == "" {
		return errResult(fmt.Errorf("id is required")), nil
	}
	tk, ok := t.Store.Get(in.ID)
	if !ok {
		return errResult(fmt.Errorf("ticket %q not found", in.ID)), nil
	}
	return jsonOut(tk), nil
}

// =====================================================================
// tickets_create — file a new ticket
// =====================================================================

type TicketCreateTool struct {
	Store *TicketStore
}

func (*TicketCreateTool) Name() string { return "tickets_create" }
func (*TicketCreateTool) Description() string {
	return "File a new support ticket. Use when the customer's issue warrants " +
		"tracking — refund requests, account compromise, billing disputes, " +
		"reported outages, or anything that may need supervisor follow-up. " +
		"Returns the new ticket ID; remember it for later updates."
}
func (*TicketCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"customer_id": {
				"type": "string",
				"description": "Customer account ID, e.g. 'C-7782'. If unknown, use 'C-UNKNOWN' and note it in the description."
			},
			"subject": {
				"type": "string",
				"description": "One-line summary, < 80 chars."
			},
			"description": {
				"type": "string",
				"description": "Full description in the customer's words plus any context the agent has gathered."
			},
			"priority": {
				"type": "string",
				"enum": ["low", "medium", "high", "urgent"],
				"description": "Default 'medium'. See escalation-matrix for thresholds."
			}
		},
		"required": ["customer_id", "subject", "description"]
	}`)
}

// Not concurrency-safe: allocates a new ID and mutates store state. The
// partitioner will serialise multiple create calls inside one batch.
func (*TicketCreateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *TicketCreateTool) Execute(_ context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		CustomerID  string `json:"customer_id"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	tk, err := t.Store.Create(in.CustomerID, in.Subject, in.Description, in.Priority)
	if err != nil {
		return errResult(err), nil
	}
	return jsonOut(map[string]any{
		"created": true,
		"ticket":  tk,
	}), nil
}

// =====================================================================
// tickets_update — change status / priority / append a note
// =====================================================================

type TicketUpdateTool struct {
	Store *TicketStore
}

func (*TicketUpdateTool) Name() string { return "tickets_update" }
func (*TicketUpdateTool) Description() string {
	return "Mutate an existing ticket. Set `status` to mark it resolved or " +
		"escalated, change `priority`, or append an `internal_note`. " +
		"Restricted to supervisor mode — non-supervisor calls are denied " +
		"by the permission checker before reaching this tool."
}
func (*TicketUpdateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "Ticket ID, e.g. 'T-1002'."
			},
			"status": {
				"type": "string",
				"enum": ["open", "pending", "resolved", "escalated"],
				"description": "Optional. Omit to leave unchanged."
			},
			"priority": {
				"type": "string",
				"enum": ["low", "medium", "high", "urgent"],
				"description": "Optional. Omit to leave unchanged."
			},
			"internal_note": {
				"type": "string",
				"description": "Optional internal note to append (not visible to the customer)."
			}
		},
		"required": ["id"]
	}`)
}
func (*TicketUpdateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *TicketUpdateTool) Execute(_ context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		ID           string  `json:"id"`
		Status       *string `json:"status,omitempty"`
		Priority     *string `json:"priority,omitempty"`
		InternalNote string  `json:"internal_note,omitempty"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	tk, err := t.Store.Update(in.ID, UpdateOpts{
		Status:   in.Status,
		Priority: in.Priority,
		Note:     in.InternalNote,
		Author:   "agent",
		Internal: true,
	})
	if err != nil {
		return errResult(err), nil
	}
	return jsonOut(map[string]any{
		"updated": true,
		"ticket":  tk,
	}), nil
}

// =====================================================================
// escalate_to_human — webhook POST (or stdout in demo mode)
// =====================================================================

type EscalateTool struct {
	// Webhook is an optional HTTPS URL to POST escalation payloads to.
	// When empty, the tool prints to stdout instead — fine for demos.
	Webhook string
}

func (*EscalateTool) Name() string { return "escalate_to_human" }
func (*EscalateTool) Description() string {
	return "Escalate a ticket to a human (Security, Billing, SRE, or Senior " +
		"Support). Always file the ticket first, then escalate referencing " +
		"its ID. Restricted to supervisor mode. The escalation note must " +
		"follow the format in `escalation-matrix`."
}
func (*EscalateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ticket_id": {
				"type": "string",
				"description": "ID of the ticket being escalated. Required."
			},
			"route_to": {
				"type": "string",
				"enum": ["Security", "Billing", "Legal", "SRE", "Senior support", "Tier 2"],
				"description": "Which queue receives the escalation."
			},
			"summary": {
				"type": "string",
				"description": "One-line summary the receiving human reads first."
			},
			"note": {
				"type": "string",
				"description": "Full escalation note formatted per escalation-matrix conventions."
			}
		},
		"required": ["ticket_id", "route_to", "summary", "note"]
	}`)
}
func (*EscalateTool) IsConcurrencySafe(json.RawMessage) bool { return false }

func (t *EscalateTool) Execute(ctx context.Context, input json.RawMessage) (tool.ToolResult, error) {
	var in struct {
		TicketID string `json:"ticket_id"`
		RouteTo  string `json:"route_to"`
		Summary  string `json:"summary"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult(fmt.Errorf("invalid input: %w", err)), nil
	}
	if in.TicketID == "" || in.RouteTo == "" || in.Summary == "" || in.Note == "" {
		return errResult(fmt.Errorf("ticket_id, route_to, summary, and note are all required")), nil
	}

	payload := map[string]any{
		"ticket_id":    in.TicketID,
		"route_to":     in.RouteTo,
		"summary":      in.Summary,
		"note":         in.Note,
		"escalated_at": time.Now().UTC().Format(time.RFC3339),
	}

	if t.Webhook == "" {
		// Demo mode: print to stdout instead of an HTTP POST so the
		// example runs without external infrastructure.
		fmt.Fprintf(jsonStdoutWriter{}, "\n[ESCALATION → %s] %s | ticket=%s\n  %s\n",
			in.RouteTo, in.Summary, in.TicketID, in.Note)
		payload["delivery"] = "stdout (no SUPPORT_AGENT_WEBHOOK configured)"
		return jsonOut(payload), nil
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Webhook, bytes.NewReader(body))
	if err != nil {
		return errResult(fmt.Errorf("build webhook request: %w", err)), nil
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "harness-support-agent/0.1")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return errResult(fmt.Errorf("webhook POST: %w", err)), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errResult(fmt.Errorf("webhook returned %d", resp.StatusCode)), nil
	}
	payload["delivery"] = fmt.Sprintf("webhook %d", resp.StatusCode)
	return jsonOut(payload), nil
}

// jsonStdoutWriter is a thin io.Writer wrapper around os.Stdout used so
// the escalation banner doesn't need its own import block; keeps the
// fmt.Fprintf call inline with the rest of the package's stdout uses.
type jsonStdoutWriter struct{}

func (jsonStdoutWriter) Write(p []byte) (int, error) {
	return fmt.Print(string(p))
}
