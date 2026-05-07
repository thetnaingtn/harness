package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// JobScheduler is the interface for scheduling recurring jobs.
// This avoids importing the cron package directly (decoupling).
// The cron.Scheduler implements this via an adapter in main.go.
//
// AddJob takes the agent ID the job will run as. Empty agentID lets the
// adapter pick a default (typically the first configured agent) — only
// useful for legacy persisted jobs and UI calls that don't know better.
type JobScheduler interface {
	AddJob(agentID, name, schedule, prompt string) error
	RemoveJob(name string) error
	ListJobs() []JobInfo
	PauseJob(name string) error
	ResumeJob(name string) error
	UpdateJobSchedule(name, schedule string) error
}

// JobInfo is a summary of a scheduled job, returned by ListJobs.
type JobInfo struct {
	Name     string `json:"name"`
	AgentID  string `json:"agentId,omitempty"`
	Schedule string `json:"schedule"`
	Prompt   string `json:"prompt"`
	Paused   bool   `json:"paused"`
}

// CronTool allows the agent to dynamically schedule recurring tasks.
//
// AgentID is the calling agent's ID, baked in at construction so jobs the
// LLM schedules execute as the agent that scheduled them. Empty is allowed
// (adapter falls back to its default agent) but should be avoided in
// production wiring.
type CronTool struct {
	AgentID   string
	Scheduler JobScheduler
}

type cronInput struct {
	Action   string `json:"action"`             // "add", "remove", or "list"
	Name     string `json:"name,omitempty"`      // job name (for add/remove)
	Schedule string `json:"schedule,omitempty"`  // interval e.g. "30m", "1h", "24h" (for add)
	Prompt   string `json:"prompt,omitempty"`    // prompt to send to the agent (for add)
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return `Schedule, stop, or list recurring tasks. Supports three actions:
- "add": Schedule a new recurring job. Requires "name" (unique identifier), "schedule" (Go duration string like "30m", "1h", "24h"), and "prompt" (the instruction to execute each interval).
- "remove": Stop and remove a scheduled job. Requires "name".
- "list": List all currently scheduled jobs.
Jobs run as the agent that scheduled them — same model, workspace, tools, and policies. You cannot schedule a job to run as a different agent.
Use this to set up automated checks, reminders, or periodic tasks.`
}

func (t *CronTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["add", "remove", "list"],
				"description": "The action to perform: add a new job, remove an existing job, or list all jobs"
			},
			"name": {
				"type": "string",
				"description": "Unique name for the job (required for add)"
			},
			"schedule": {
				"type": "string",
				"description": "How often to run, as a Go duration string (e.g. \"30m\", \"1h\", \"24h\") (required for add)"
			},
			"prompt": {
				"type": "string",
				"description": "The prompt/instruction to execute each interval (required for add)"
			}
		},
		"required": ["action"]
	}`)
}

// IsConcurrencySafe returns false — cron mutates the scheduler's job list.
func (t *CronTool) IsConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *CronTool) Execute(ctx context.Context, input json.RawMessage) (ToolResult, error) {
	var in cronInput
	if err := json.Unmarshal(input, &in); err != nil {
		return ToolResult{Error: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.Scheduler == nil {
		return ToolResult{Error: "cron scheduling is not available"}, nil
	}

	switch in.Action {
	case "add":
		return t.addJob(in)
	case "remove":
		return t.removeJob(in)
	case "list":
		return t.listJobs()
	default:
		return ToolResult{Error: fmt.Sprintf("unknown action: %q (valid: add, remove, list)", in.Action)}, nil
	}
}

func (t *CronTool) addJob(in cronInput) (ToolResult, error) {
	if in.Name == "" {
		return ToolResult{Error: "name is required for add action"}, nil
	}
	if in.Schedule == "" {
		return ToolResult{Error: "schedule is required for add action"}, nil
	}
	if in.Prompt == "" {
		return ToolResult{Error: "prompt is required for add action"}, nil
	}

	if err := t.Scheduler.AddJob(t.AgentID, in.Name, in.Schedule, in.Prompt); err != nil {
		return ToolResult{Error: fmt.Sprintf("failed to schedule job: %v", err)}, nil
	}

	msg := fmt.Sprintf("Scheduled job %q to run every %s", in.Name, in.Schedule)
	if t.AgentID != "" {
		msg += fmt.Sprintf(" as agent %q", t.AgentID)
	}
	return ToolResult{
		Output: msg,
		Metadata: map[string]any{
			"name":     in.Name,
			"schedule": in.Schedule,
			"agentId":  t.AgentID,
		},
	}, nil
}

func (t *CronTool) removeJob(in cronInput) (ToolResult, error) {
	if in.Name == "" {
		return ToolResult{Error: "name is required for remove action"}, nil
	}

	if err := t.Scheduler.RemoveJob(in.Name); err != nil {
		return ToolResult{Error: fmt.Sprintf("failed to remove job: %v", err)}, nil
	}

	return ToolResult{
		Output: fmt.Sprintf("Removed job %q", in.Name),
	}, nil
}

func (t *CronTool) listJobs() (ToolResult, error) {
	jobs := t.Scheduler.ListJobs()

	if len(jobs) == 0 {
		return ToolResult{Output: "No scheduled jobs."}, nil
	}

	out, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return ToolResult{Error: fmt.Sprintf("failed to marshal jobs: %v", err)}, nil
	}

	return ToolResult{
		Output: fmt.Sprintf("%d scheduled job(s):\n%s", len(jobs), string(out)),
		Metadata: map[string]any{
			"count": len(jobs),
		},
	}, nil
}
