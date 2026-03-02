package tools

import (
	"fmt"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

func (dm *DelegateManager) emitEvent(name string, task *DelegationTask) {
	if dm.msgBus == nil {
		return
	}
	payload := map[string]string{
		"delegation_id": task.ID,
		"source_agent":  task.SourceAgentID.String(),
		"target_agent":  task.TargetAgentKey,
		"user_id":       task.UserID,
		"mode":          task.Mode,
	}
	if task.TeamID.String() != "00000000-0000-0000-0000-000000000000" {
		payload["team_id"] = task.TeamID.String()
	}
	if task.TeamTaskID.String() != "00000000-0000-0000-0000-000000000000" {
		payload["team_task_id"] = task.TeamTaskID.String()
	}
	dm.msgBus.Broadcast(bus.Event{
		Name:    name,
		Payload: payload,
	})
}

func formatDelegateAnnounce(task *DelegationTask, artifacts *DelegateArtifacts, err error, elapsed time.Duration) string {
	if err != nil && len(artifacts.Results) == 0 {
		return fmt.Sprintf(
			"[System Message] All delegations finished. The last delegation to agent %q failed.\n\nError: %s\n\nStats: runtime %s\n\n"+
				"Handle the failed task yourself or try a different agent.",
			task.TargetAgentKey, err.Error(), elapsed.Round(time.Millisecond))
	}

	msg := "[System Message] All team delegations completed.\n\n"

	// Render each delegation result
	for i, r := range artifacts.Results {
		msg += fmt.Sprintf("--- Result from %q ---\n%s\n", r.AgentKey, r.Content)
		if r.HasMedia {
			msg += "[media file(s) attached — will be delivered automatically. Do NOT recreate or call create_image.]\n"
		}
		if i < len(artifacts.Results)-1 {
			msg += "\n"
		}
	}

	msg += fmt.Sprintf("\nStats: total elapsed %s\n\n", elapsed.Round(time.Millisecond))
	msg += "Review the results above. You may:\n" +
		"- Present a comprehensive summary to the user (if the task is fully done)\n" +
		"- Delegate follow-up tasks to refine, combine, or extend these results\n" +
		"- Ask a member to revise based on another member's output\n" +
		"Any media files attached will be delivered automatically — do NOT recreate them."

	return msg
}
