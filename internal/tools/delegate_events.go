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
	dm.msgBus.Broadcast(bus.Event{
		Name: name,
		Payload: map[string]string{
			"delegation_id": task.ID,
			"source_agent":  task.SourceAgentID.String(),
			"target_agent":  task.TargetAgentKey,
			"user_id":       task.UserID,
			"mode":          task.Mode,
		},
	})
}

func formatDelegateAnnounce(task *DelegationTask, result *DelegateRunResult, err error, elapsed time.Duration) string {
	if err != nil {
		return fmt.Sprintf(
			"[System Message] Delegation to agent %q failed.\n\nError: %s\n\nStats: runtime %s\n\n"+
				"Handle the task yourself or try a different agent.",
			task.TargetAgentKey, err.Error(), elapsed.Round(time.Millisecond))
	}
	return fmt.Sprintf(
		"[System Message] Delegation to agent %q completed.\n\nResult:\n%s\n\nStats: runtime %s, iterations %d\n\n"+
			"Convert the result above into your normal assistant voice and send that user-facing update now. "+
			"Keep internal details private. Reply ONLY: NO_REPLY if this exact result was already delivered to the user.",
		task.TargetAgentKey, result.Content, elapsed.Round(time.Millisecond), result.Iterations)
}
