package tools

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// Cancel cancels a running delegation by ID.
func (dm *DelegateManager) Cancel(delegationID string) bool {
	val, ok := dm.active.Load(delegationID)
	if !ok {
		return false
	}
	task := val.(*DelegationTask)
	if task.cancelFunc != nil {
		task.cancelFunc()
	}
	task.Status = "cancelled"
	now := time.Now()
	task.CompletedAt = &now
	dm.active.Delete(delegationID)
	dm.emitEvent("delegation.cancelled", task)
	slog.Info("delegation cancelled", "id", delegationID, "target", task.TargetAgentKey)
	return true
}

// ListActive returns all active delegations for a source agent.
func (dm *DelegateManager) ListActive(sourceAgentID uuid.UUID) []*DelegationTask {
	var tasks []*DelegationTask
	dm.active.Range(func(_, val any) bool {
		t := val.(*DelegationTask)
		if t.SourceAgentID == sourceAgentID && t.Status == "running" {
			tasks = append(tasks, t)
		}
		return true
	})
	return tasks
}

// ActiveCountForLink counts running delegations for a specific source→target pair.
func (dm *DelegateManager) ActiveCountForLink(sourceID, targetID uuid.UUID) int {
	count := 0
	dm.active.Range(func(_, val any) bool {
		t := val.(*DelegationTask)
		if t.SourceAgentID == sourceID && t.TargetAgentID == targetID && t.Status == "running" {
			count++
		}
		return true
	})
	return count
}

// ActiveCountForTarget counts running delegations targeting a specific agent from all sources.
func (dm *DelegateManager) ActiveCountForTarget(targetID uuid.UUID) int {
	count := 0
	dm.active.Range(func(_, val any) bool {
		t := val.(*DelegationTask)
		if t.TargetAgentID == targetID && t.Status == "running" {
			count++
		}
		return true
	})
	return count
}

// trackCompleted records a delegate session key for deferred cleanup.
func (dm *DelegateManager) trackCompleted(task *DelegationTask) {
	if dm.sessionStore == nil {
		return
	}
	dm.completedMu.Lock()
	dm.completedSessions = append(dm.completedSessions, task.SessionKey)
	dm.completedMu.Unlock()
}

// flushCompletedSessions deletes all tracked delegate sessions.
func (dm *DelegateManager) flushCompletedSessions() {
	if dm.sessionStore == nil {
		return
	}
	dm.completedMu.Lock()
	sessions := dm.completedSessions
	dm.completedSessions = nil
	dm.completedMu.Unlock()

	for _, key := range sessions {
		if err := dm.sessionStore.Delete(key); err != nil {
			slog.Warn("delegate: session cleanup failed", "session", key, "error", err)
		}
	}
	if len(sessions) > 0 {
		slog.Info("delegate: cleaned up sessions", "count", len(sessions))
	}
}

// autoCompleteTeamTask attempts to claim+complete the associated team task.
// Called after a delegation finishes successfully. Errors are logged but not fatal.
// On success, flushes all tracked delegate sessions (task done = context no longer needed).
func (dm *DelegateManager) autoCompleteTeamTask(task *DelegationTask, resultContent string) {
	if dm.teamStore == nil || task.TeamTaskID == uuid.Nil {
		return
	}
	_ = dm.teamStore.ClaimTask(context.Background(), task.TeamTaskID, task.TargetAgentID)
	if err := dm.teamStore.CompleteTask(context.Background(), task.TeamTaskID, resultContent); err != nil {
		slog.Warn("delegate: failed to auto-complete team task",
			"task_id", task.TeamTaskID, "delegation_id", task.ID, "error", err)
	} else {
		slog.Info("delegate: auto-completed team task",
			"task_id", task.TeamTaskID, "delegation_id", task.ID)
		// Task done — flush delegate sessions
		dm.flushCompletedSessions()
	}
}

// saveDelegationHistory persists a delegation record to the database.
// Called after delegation completes (success, fail, or cancel). Errors are logged, not fatal.
func (dm *DelegateManager) saveDelegationHistory(task *DelegationTask, resultContent string, delegateErr error, duration time.Duration) {
	if dm.teamStore == nil {
		return
	}

	record := &store.DelegationHistoryData{
		SourceAgentID: task.SourceAgentID,
		TargetAgentID: task.TargetAgentID,
		UserID:        task.UserID,
		Task:          task.Task,
		Mode:          task.Mode,
		Iterations:    0,
		DurationMS:    int(duration.Milliseconds()),
	}

	if task.TeamTaskID != uuid.Nil {
		record.TeamTaskID = &task.TeamTaskID
	}
	if task.OriginTraceID != uuid.Nil {
		record.TraceID = &task.OriginTraceID
	}

	now := time.Now()
	record.CompletedAt = &now

	if delegateErr != nil {
		record.Status = "failed"
		errStr := delegateErr.Error()
		record.Error = &errStr
	} else {
		record.Status = "completed"
		record.Result = &resultContent
	}

	if err := dm.teamStore.SaveDelegationHistory(context.Background(), record); err != nil {
		slog.Warn("delegate: failed to save delegation history",
			"delegation_id", task.ID, "error", err)
	}
}
