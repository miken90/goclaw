package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// CronTool lets agents manage Gateway cron jobs.
// Matching OpenClaw src/agents/tools/cron-tool.ts.
type CronTool struct {
	cronStore store.CronStore
}

func NewCronTool(cronStore store.CronStore) *CronTool {
	return &CronTool{cronStore: cronStore}
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return `Manage Gateway cron jobs (status/list/add/update/remove/run/runs).

ACTIONS:
- status: Check cron scheduler status
- list: List jobs (use includeDisabled:true to include disabled)
- add: Create job (requires job object, see schema below)
- update: Modify job (requires jobId + patch object)
- remove: Delete job (requires jobId)
- run: Trigger job immediately (requires jobId)
- runs: Get job run history (requires jobId)

JOB SCHEMA (for add action):
{
  "name": "string (required, lowercase slug)",
  "schedule": { ... },      // Required: when to run
  "message": "string",      // Required: what message to send to the agent
  "deliver": true|false,    // Optional: deliver result to channel (default false)
  "channel": "telegram",    // Optional: target channel for delivery
  "to": "chat-id",          // Optional: target chat/recipient ID
  "agentId": "agent-uuid",  // Optional: which agent handles the job (default: current)
  "deleteAfterRun": true    // Optional: auto-delete after execution (default true for "at" schedule)
}

SCHEDULE TYPES (schedule.kind):
- "at": One-shot at absolute time
  { "kind": "at", "atMs": <unix-milliseconds> }
- "every": Recurring interval
  { "kind": "every", "everyMs": <interval-ms> }
- "cron": Cron expression
  { "kind": "cron", "expr": "<5-field cron expression>", "tz": "<optional-timezone>" }

CRITICAL CONSTRAINTS:
- name must be a valid slug (lowercase letters, numbers, hyphens only)
- message is required for add action
- schedule is required for add action
- Default: jobs run as isolated agent turns with the specified message

Use jobId as the canonical identifier; id is accepted for compatibility.`
}

func (t *CronTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "The cron action to perform",
				"enum":        []string{"status", "list", "add", "update", "remove", "run", "runs"},
			},
			"includeDisabled": map[string]interface{}{
				"type":        "boolean",
				"description": "Include disabled jobs in list (default false)",
			},
			"job": map[string]interface{}{
				"type":        "object",
				"description": "Job definition for add action (name, schedule, message, deliver, channel, to, agentId, deleteAfterRun)",
				"additionalProperties": true,
			},
			"jobId": map[string]interface{}{
				"type":        "string",
				"description": "Job ID for update/remove/run/runs actions",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Backward compatibility alias for jobId",
			},
			"patch": map[string]interface{}{
				"type":        "object",
				"description": "Patch object for update action",
				"additionalProperties": true,
			},
			"runMode": map[string]interface{}{
				"type":        "string",
				"description": "Run mode: 'due' (only if due) or 'force' (immediate)",
				"enum":        []string{"due", "force"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *CronTool) Execute(ctx context.Context, args map[string]interface{}) *Result {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action parameter is required")
	}

	switch action {
	case "status":
		return t.handleStatus()
	case "list":
		return t.handleList(args)
	case "add":
		return t.handleAdd(args)
	case "update":
		return t.handleUpdate(args)
	case "remove":
		return t.handleRemove(args)
	case "run":
		return t.handleRun(args)
	case "runs":
		return t.handleRuns(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

func (t *CronTool) handleStatus() *Result {
	status := t.cronStore.Status()
	data, _ := json.MarshalIndent(status, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleList(args map[string]interface{}) *Result {
	includeDisabled, _ := args["includeDisabled"].(bool)
	jobs := t.cronStore.ListJobs(includeDisabled)

	result := map[string]interface{}{
		"jobs":  jobs,
		"count": len(jobs),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleAdd(args map[string]interface{}) *Result {
	jobObj, ok := args["job"].(map[string]interface{})
	if !ok {
		return ErrorResult("job object is required for add action")
	}

	name, _ := jobObj["name"].(string)
	if name == "" {
		return ErrorResult("job.name is required")
	}

	scheduleObj, ok := jobObj["schedule"].(map[string]interface{})
	if !ok {
		return ErrorResult("job.schedule is required")
	}

	message, _ := jobObj["message"].(string)
	if message == "" {
		return ErrorResult("job.message is required")
	}

	// Parse schedule
	schedule := store.CronSchedule{
		Kind: stringFromMap(scheduleObj, "kind"),
	}
	if schedule.Kind == "" {
		return ErrorResult("job.schedule.kind is required (at, every, or cron)")
	}

	switch schedule.Kind {
	case "at":
		if v, ok := numberFromMap(scheduleObj, "atMs"); ok {
			ms := int64(v)
			schedule.AtMS = &ms
		} else {
			return ErrorResult("job.schedule.atMs is required for 'at' schedule")
		}
	case "every":
		if v, ok := numberFromMap(scheduleObj, "everyMs"); ok {
			ms := int64(v)
			schedule.EveryMS = &ms
		} else {
			return ErrorResult("job.schedule.everyMs is required for 'every' schedule")
		}
	case "cron":
		schedule.Expr = stringFromMap(scheduleObj, "expr")
		if schedule.Expr == "" {
			return ErrorResult("job.schedule.expr is required for 'cron' schedule")
		}
		schedule.TZ = stringFromMap(scheduleObj, "tz")
	default:
		return ErrorResult(fmt.Sprintf("invalid schedule kind: %s (must be at, every, or cron)", schedule.Kind))
	}

	// Optional fields
	deliver, _ := jobObj["deliver"].(bool)
	channel, _ := jobObj["channel"].(string)
	to, _ := jobObj["to"].(string)
	agentID, _ := jobObj["agentId"].(string)

	job, err := t.cronStore.AddJob(name, schedule, message, deliver, channel, to, agentID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create cron job: %v", err))
	}

	data, _ := json.MarshalIndent(map[string]interface{}{"job": job}, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleUpdate(args map[string]interface{}) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for update action")
	}

	patchObj, ok := args["patch"].(map[string]interface{})
	if !ok {
		return ErrorResult("patch object is required for update action")
	}

	var patch store.CronJobPatch
	// Re-marshal and unmarshal to leverage JSON tags
	patchJSON, _ := json.Marshal(patchObj)
	json.Unmarshal(patchJSON, &patch)

	job, err := t.cronStore.UpdateJob(jobID, patch)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to update cron job: %v", err))
	}

	data, _ := json.MarshalIndent(map[string]interface{}{"job": job}, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRemove(args map[string]interface{}) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for remove action")
	}

	if err := t.cronStore.RemoveJob(jobID); err != nil {
		return ErrorResult(fmt.Sprintf("failed to remove cron job: %v", err))
	}

	data, _ := json.MarshalIndent(map[string]interface{}{"deleted": true, "jobId": jobID}, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRun(args map[string]interface{}) *Result {
	jobID := resolveJobID(args)
	if jobID == "" {
		return ErrorResult("jobId is required for run action")
	}

	runMode, _ := args["runMode"].(string)
	force := runMode == "force"

	ran, reason, err := t.cronStore.RunJob(jobID, force)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to run cron job: %v", err))
	}

	result := map[string]interface{}{
		"ran":   ran,
		"jobId": jobID,
	}
	if !ran && reason != "" {
		result["reason"] = reason
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

func (t *CronTool) handleRuns(args map[string]interface{}) *Result {
	jobID := resolveJobID(args)

	limit := 20
	if v, ok := numberFromMap(args, "limit"); ok {
		limit = int(v)
	}

	entries := t.cronStore.GetRunLog(jobID, limit)

	result := map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	return NewResult(string(data))
}

// --- helpers ---

func resolveJobID(args map[string]interface{}) string {
	if id, ok := args["jobId"].(string); ok && id != "" {
		return id
	}
	if id, ok := args["id"].(string); ok && id != "" {
		return id
	}
	return ""
}

func stringFromMap(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func numberFromMap(m map[string]interface{}, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}
