// SupportChecker implements tool.PermissionChecker with one rule: the
// two write-side tools (tickets_update, escalate_to_human) are visible
// and callable only in supervisor mode. Read tools and the auto-
// registered load_skill are always allowed.
//
// FilterToolDefs runs at tool-list-assembly time, so in non-supervisor
// mode the LLM never sees the gated tools and won't waste a turn
// inventing calls that would just be denied.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/tool"
)

// SupportChecker enforces the read/write split.
type SupportChecker struct {
	supervisor bool
}

// NewSupportChecker constructs the checker. supervisor=true unlocks
// tickets_update and escalate_to_human; supervisor=false hides them.
func NewSupportChecker(supervisor bool) *SupportChecker {
	return &SupportChecker{supervisor: supervisor}
}

// gatedTools is the set restricted to supervisor mode. Listed
// explicitly (rather than "anything matching `*_update`") so adding a
// new write tool is an obvious diff: the author has to decide whether
// to add it here.
var gatedTools = map[string]bool{
	"tickets_update":     true,
	"escalate_to_human":  true,
}

// Check is consulted before every tool invocation.
func (c *SupportChecker) Check(_ context.Context, _agentID, toolName string, _ json.RawMessage) tool.Decision {
	if !gatedTools[toolName] {
		return tool.Decision{Behavior: tool.DecisionAllow}
	}
	if c.supervisor {
		return tool.Decision{Behavior: tool.DecisionAllow}
	}
	return tool.Decision{
		Behavior: tool.DecisionDeny,
		Reason: fmt.Sprintf("%q is a write tool and requires supervisor mode "+
			"(re-run with --supervisor or SUPPORT_AGENT_SUPERVISOR=1)", toolName),
	}
}

// FilterToolDefs hides gated tools from the LLM's tool list when not in
// supervisor mode. Mirrors Check's policy so visibility and
// executability never disagree.
func (c *SupportChecker) FilterToolDefs(toolDefs []llm.ToolDef, _agentID string) []llm.ToolDef {
	if c.supervisor {
		return toolDefs
	}
	out := make([]llm.ToolDef, 0, len(toolDefs))
	for _, td := range toolDefs {
		if !gatedTools[td.Name] {
			out = append(out, td)
		}
	}
	return out
}
