// Pure-Go unit tests for the offline pieces (store, permission,
// skills, kb_search). The agent loop itself isn't exercised here — it
// needs an LLM and is covered by interactive use.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sausheong/harness/llm"
)

func TestTicketStore_GetSeed(t *testing.T) {
	s := NewTicketStore()
	s.Seed()
	got, ok := s.Get("T-1002")
	if !ok {
		t.Fatal("seed ticket T-1002 missing")
	}
	if got.Status != StatusPending {
		t.Errorf("T-1002 status = %q, want %q", got.Status, StatusPending)
	}
}

func TestTicketStore_GetReturnsCopy(t *testing.T) {
	s := NewTicketStore()
	s.Seed()
	t1, _ := s.Get("T-1001")
	t1.Status = "tampered"
	t2, _ := s.Get("T-1001")
	if t2.Status == "tampered" {
		t.Error("Get returned a live pointer; mutations leaked")
	}
}

func TestTicketStore_CreateAndUpdate(t *testing.T) {
	s := NewTicketStore()
	tk, err := s.Create("C-1", "Cannot log in", "got 403", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.ID, "T-") {
		t.Errorf("id = %q, want T- prefix", tk.ID)
	}
	if tk.Priority != "medium" {
		t.Errorf("default priority = %q, want medium", tk.Priority)
	}

	resolved := StatusResolved
	updated, err := s.Update(tk.ID, UpdateOpts{
		Status: &resolved,
		Note:   "fixed via password reset",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusResolved {
		t.Errorf("status not updated: %q", updated.Status)
	}
	if len(updated.Notes) != 1 || updated.Notes[0].Content != "fixed via password reset" {
		t.Errorf("note not appended: %+v", updated.Notes)
	}
}

func TestTicketStore_RejectsInvalidPriority(t *testing.T) {
	s := NewTicketStore()
	if _, err := s.Create("C-1", "subj", "desc", "critical"); err == nil {
		t.Error("expected priority validation error")
	}
}

func TestSupportChecker_Default_HidesGated(t *testing.T) {
	c := NewSupportChecker(false)
	defs := []llm.ToolDef{
		{Name: "kb_search"},
		{Name: "tickets_get"},
		{Name: "tickets_update"},
		{Name: "escalate_to_human"},
		{Name: "load_skill"},
	}
	out := c.FilterToolDefs(defs, "support")
	names := map[string]bool{}
	for _, d := range out {
		names[d.Name] = true
	}
	if names["tickets_update"] || names["escalate_to_human"] {
		t.Error("non-supervisor mode leaked write tools into the LLM tool list")
	}
	for _, want := range []string{"kb_search", "tickets_get", "load_skill"} {
		if !names[want] {
			t.Errorf("non-supervisor mode hid read tool %q", want)
		}
	}
}

func TestSupportChecker_DeniesGatedAtCallTime(t *testing.T) {
	c := NewSupportChecker(false)
	d := c.Check(context.Background(), "support", "tickets_update", nil)
	if d.Behavior != 1 { // tool.DecisionDeny
		t.Errorf("expected Deny, got %v", d.Behavior)
	}
	if !strings.Contains(d.Reason, "supervisor") {
		t.Errorf("deny reason should mention supervisor mode: %q", d.Reason)
	}
}

func TestSupportChecker_Supervisor_AllowsAll(t *testing.T) {
	c := NewSupportChecker(true)
	for _, name := range []string{"kb_search", "tickets_update", "escalate_to_human"} {
		if c.Check(context.Background(), "support", name, nil).Behavior != 0 {
			t.Errorf("supervisor mode denied %q", name)
		}
	}
}

func TestKBSkills_FormatIndexAndGet(t *testing.T) {
	k := NewKBSkills()
	idx := k.FormatIndex()
	for _, want := range []string{"refund-policy", "escalation-matrix", "account-recovery", "password-reset", "api-rate-limits"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index missing %q", want)
		}
	}
	body, ok := k.Get("refund-policy")
	if !ok || !strings.Contains(body, "Refund Policy") {
		t.Errorf("Get(refund-policy) returned ok=%v body-prefix=%q", ok, firstN(body, 60))
	}
	if _, ok := k.Get("does-not-exist"); ok {
		t.Error("Get on unknown slug returned ok=true")
	}
}

func TestKBSearchTool_FindsByBodyContent(t *testing.T) {
	tool := &KBSearchTool{KB: NewKBSkills()}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"chargeback"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	if !strings.Contains(out.Output, "refund-policy") {
		t.Errorf("expected refund-policy hit for 'chargeback': %s", firstN(out.Output, 200))
	}
}

func TestKBSearchTool_ShortQueryRejected(t *testing.T) {
	tool := &KBSearchTool{KB: NewKBSkills()}
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{"query":"a"}`))
	if out.Error == "" {
		t.Error("expected error for 1-char query")
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
