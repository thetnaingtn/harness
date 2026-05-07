// In-memory ticket store. Goroutine-safe via a sync.RWMutex; the IDs are
// monotonically allocated so a session can refer to "the ticket I just
// created" without round-tripping through the model.
package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Ticket statuses. Stored as plain strings (rather than a custom enum
// type) so the JSON the agent sees is self-describing.
const (
	StatusOpen      = "open"
	StatusPending   = "pending"
	StatusResolved  = "resolved"
	StatusEscalated = "escalated"
)

// Note is one entry in a ticket's history. Internal=true marks a note
// the customer wouldn't see — useful for the agent to record what it
// did between turns.
type Note struct {
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	Internal  bool      `json:"internal"`
	CreatedAt time.Time `json:"created_at"`
}

// Ticket is the unit the agent reads, creates, and (in supervisor mode)
// updates. Mirrors the fields a real ticketing system exposes; status +
// priority are constrained strings rather than enums for JSON simplicity.
type Ticket struct {
	ID          string    `json:"id"`
	CustomerID  string    `json:"customer_id"`
	Subject     string    `json:"subject"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	Priority    string    `json:"priority"` // low | medium | high | urgent
	Notes       []Note    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TicketStore is the in-memory backend shared by all ticket-* tools.
type TicketStore struct {
	mu     sync.RWMutex
	byID   map[string]*Ticket
	nextID int
}

// NewTicketStore allocates an empty store with IDs starting at T-1001.
func NewTicketStore() *TicketStore {
	return &TicketStore{
		byID:   make(map[string]*Ticket),
		nextID: 1001,
	}
}

// Get returns a copy of the ticket so callers can't mutate the stored
// value through the returned pointer.
func (s *TicketStore) Get(id string) (*Ticket, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	cp := *t
	cp.Notes = append([]Note(nil), t.Notes...) // defensive copy
	return &cp, true
}

// Create allocates a new ticket id and stores the record. customerID,
// subject, and description are required; defaults are applied to status
// and priority when blank.
func (s *TicketStore) Create(customerID, subject, description, priority string) (*Ticket, error) {
	if customerID == "" {
		return nil, errors.New("customer_id is required")
	}
	if subject == "" {
		return nil, errors.New("subject is required")
	}
	if description == "" {
		return nil, errors.New("description is required")
	}
	if priority == "" {
		priority = "medium"
	}
	if !validPriority(priority) {
		return nil, fmt.Errorf("invalid priority %q (allowed: low, medium, high, urgent)", priority)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	id := fmt.Sprintf("T-%d", s.nextID)
	s.nextID++
	now := time.Now().UTC()
	t := &Ticket{
		ID:          id,
		CustomerID:  customerID,
		Subject:     subject,
		Description: description,
		Status:      StatusOpen,
		Priority:    priority,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.byID[id] = t
	cp := *t
	return &cp, nil
}

// UpdateOpts selects which fields a TicketStore.Update call mutates.
// Unset pointers leave the field unchanged. Note (when non-empty) is
// always appended in addition to any field changes.
type UpdateOpts struct {
	Status   *string
	Priority *string
	Note     string
	Author   string
	Internal bool
}

// Update mutates an existing ticket. Returns the post-update copy or an
// error if the id is unknown / the field values are invalid.
func (s *TicketStore) Update(id string, opts UpdateOpts) (*Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("ticket %q not found", id)
	}
	if opts.Status != nil {
		if !validStatus(*opts.Status) {
			return nil, fmt.Errorf("invalid status %q (allowed: open, pending, resolved, escalated)", *opts.Status)
		}
		t.Status = *opts.Status
	}
	if opts.Priority != nil {
		if !validPriority(*opts.Priority) {
			return nil, fmt.Errorf("invalid priority %q (allowed: low, medium, high, urgent)", *opts.Priority)
		}
		t.Priority = *opts.Priority
	}
	if opts.Note != "" {
		author := opts.Author
		if author == "" {
			author = "agent"
		}
		t.Notes = append(t.Notes, Note{
			Author:    author,
			Content:   opts.Note,
			Internal:  opts.Internal,
			CreatedAt: time.Now().UTC(),
		})
	}
	t.UpdatedAt = time.Now().UTC()
	cp := *t
	cp.Notes = append([]Note(nil), t.Notes...)
	return &cp, nil
}

// All returns every ticket, sorted by id (ascending). Used only by tests
// today; could back a future tickets_list tool.
func (s *TicketStore) All() []Ticket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Ticket, 0, len(s.byID))
	for _, t := range s.byID {
		cp := *t
		cp.Notes = append([]Note(nil), t.Notes...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Seed pre-populates four tickets covering the main scenarios the demo
// prompts surface. Idempotent — safe to call once at startup.
func (s *TicketStore) Seed() {
	now := time.Now().UTC()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	seeds := []*Ticket{
		{
			ID: "T-1001", CustomerID: "C-7782",
			Subject: "Refund for last month's overage charge",
			Description: "Customer says the $87 overage on April invoice was unexpected; " +
				"hadn't seen the rate-limit warnings.",
			Status: StatusOpen, Priority: "medium",
			CreatedAt: ago(36 * time.Hour), UpdatedAt: ago(36 * time.Hour),
		},
		{
			ID: "T-1002", CustomerID: "C-2294",
			Subject: "API returning 401s after key rotation",
			Description: "Customer rotated their API key in the dashboard but the new key " +
				"returns 401 from /v1/messages. Old key still works.",
			Status: StatusPending, Priority: "high",
			Notes: []Note{{
				Author: "agent", Content: "Confirmed propagation delay; ETA 5 minutes.",
				Internal: true, CreatedAt: ago(2 * time.Hour),
			}},
			CreatedAt: ago(3 * time.Hour), UpdatedAt: ago(2 * time.Hour),
		},
		{
			ID: "T-1003", CustomerID: "C-9911",
			Subject: "Possible account compromise — unfamiliar logins",
			Description: "Customer reports two login events from IPs in countries " +
				"they've never visited. Wants the account locked pending investigation.",
			Status: StatusEscalated, Priority: "urgent",
			Notes: []Note{{
				Author: "supervisor", Content: "Account suspended; security team paged.",
				Internal: true, CreatedAt: ago(20 * time.Minute),
			}},
			CreatedAt: ago(45 * time.Minute), UpdatedAt: ago(20 * time.Minute),
		},
		{
			ID: "T-1004", CustomerID: "C-4451",
			Subject: "Resolved: password reset not arriving",
			Description: "Customer wasn't receiving the reset email; turned out to be " +
				"a corporate filter blocking transactional senders.",
			Status: StatusResolved, Priority: "low",
			CreatedAt: ago(72 * time.Hour), UpdatedAt: ago(60 * time.Hour),
		},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range seeds {
		s.byID[t.ID] = t
	}
}

func validStatus(s string) bool {
	switch s {
	case StatusOpen, StatusPending, StatusResolved, StatusEscalated:
		return true
	}
	return false
}

func validPriority(p string) bool {
	switch p {
	case "low", "medium", "high", "urgent":
		return true
	}
	return false
}
