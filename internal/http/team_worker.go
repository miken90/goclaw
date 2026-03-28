package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// TeamWorkerHandler exposes REST endpoints for external VPS workers
// to poll, claim, progress, and complete team tasks.
// Auth bypasses the admin-only WS RPC policy — requires operator role.
type TeamWorkerHandler struct {
	teamStore store.TeamStore
}

func NewTeamWorkerHandler(teamStore store.TeamStore) *TeamWorkerHandler {
	return &TeamWorkerHandler{teamStore: teamStore}
}

func (h *TeamWorkerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/teams/{teamId}/worker/tasks", h.workerAuth(h.handleListTasks))
	mux.HandleFunc("GET /v1/teams/{teamId}/worker/tasks/{taskId}", h.workerAuth(h.handleGetTask))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/tasks/{taskId}/claim", h.workerAuth(h.handleClaimTask))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/tasks/{taskId}/progress", h.workerAuth(h.handleProgress))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/tasks/{taskId}/comment", h.workerAuth(h.handleComment))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/tasks/{taskId}/complete", h.workerAuth(h.handleComplete))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/tasks/{taskId}/fail", h.workerAuth(h.handleFail))
	mux.HandleFunc("POST /v1/teams/{teamId}/worker/heartbeat", h.workerAuth(h.handleHeartbeat))
}

func (h *TeamWorkerHandler) workerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := resolveAuth(r)
		if !auth.Authenticated {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if !permissions.HasMinRole(auth.Role, permissions.RoleOperator) {
			slog.Warn("security.worker_insufficient_role", "role", auth.Role, "ip", r.RemoteAddr)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "worker requires operator role"})
			return
		}
		ctx := enrichContext(r.Context(), r, auth)
		next(w, r.WithContext(ctx))
	}
}

func (h *TeamWorkerHandler) checkStore(w http.ResponseWriter, r *http.Request) bool {
	if h.teamStore == nil {
		locale := extractLocale(r)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": i18n.T(locale, i18n.MsgTeamsNotConfigured)})
		return false
	}
	return true
}

func (h *TeamWorkerHandler) parseTeamID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("teamId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid team id"})
		return uuid.Nil, false
	}
	return id, true
}

func (h *TeamWorkerHandler) parseTaskID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("taskId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid task id"})
		return uuid.Nil, false
	}
	return id, true
}

// GET /v1/teams/{teamId}/worker/tasks?status=pending
func (h *TeamWorkerHandler) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}

	statusFilter := r.URL.Query().Get("status")
	executionTarget := r.URL.Query().Get("execution_target")

	// Use "active" filter to get pending+in_progress+blocked, then filter in handler
	tasks, err := h.teamStore.ListTasks(r.Context(), teamID, "", store.TeamTaskFilterActive, "", "", "", 100, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Filter to requested status (default: pending only)
	if statusFilter == "" {
		statusFilter = store.TeamTaskStatusPending
	}
	filtered := make([]store.TeamTaskData, 0, len(tasks))
	for _, t := range tasks {
		if t.Status != statusFilter {
			continue
		}
		// Optional execution_target filter: match metadata.execution_target
		if executionTarget != "" {
			if et, ok := t.Metadata["execution_target"].(string); !ok || et != executionTarget {
				continue
			}
		}
		filtered = append(filtered, t)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": filtered,
		"count": len(filtered),
	})
}

// GET /v1/teams/{teamId}/worker/tasks/{taskId}
func (h *TeamWorkerHandler) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	task, err := h.teamStore.GetTask(r.Context(), taskID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if task == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

// POST /v1/teams/{teamId}/worker/tasks/{taskId}/claim
func (h *TeamWorkerHandler) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	var req struct {
		AgentID  string `json:"agent_id"`
		WorkerID string `json:"worker_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	var agentID uuid.UUID
	if req.AgentID != "" {
		var err error
		agentID, err = uuid.Parse(req.AgentID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent_id"})
			return
		}
	} else {
		// Unassigned task — pick first team member as owner
		members, err := h.teamStore.ListMembers(r.Context(), teamID)
		if err == nil && len(members) > 0 {
			agentID = members[0].AgentID
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id required (no team members found)"})
			return
		}
	}

	slog.Info("worker.claim_task", "task_id", taskID, "agent_id", agentID, "worker_id", req.WorkerID, "team_id", teamID)

	// Pre-check: verify task exists and is pending (for better error messages)
	task, err := h.teamStore.GetTask(r.Context(), taskID)
	if err != nil {
		slog.Warn("worker.claim_task_not_found", "task_id", taskID, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}
	if task.Status != store.TeamTaskStatusPending {
		slog.Warn("worker.claim_task_wrong_status", "task_id", taskID, "status", task.Status, "task_number", task.TaskNumber)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "task status is " + task.Status + ", not pending"})
		return
	}

	if err := h.teamStore.ClaimTask(r.Context(), taskID, agentID, teamID); err != nil {
		if isConflictError(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task already claimed or not pending"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	task, err = h.teamStore.GetTask(r.Context(), taskID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

// POST /v1/teams/{teamId}/worker/tasks/{taskId}/progress
func (h *TeamWorkerHandler) handleProgress(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	var req struct {
		Percent int    `json:"percent"`
		Step    string `json:"step"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if err := h.teamStore.UpdateTaskProgress(r.Context(), taskID, teamID, req.Percent, req.Step); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /v1/teams/{teamId}/worker/tasks/{taskId}/comment
func (h *TeamWorkerHandler) handleComment(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	var req struct {
		Content     string `json:"content"`
		AgentID     string `json:"agent_id"`
		CommentType string `json:"comment_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	if req.CommentType == "" {
		req.CommentType = "note"
	}

	comment := &store.TeamTaskCommentData{
		ID:          uuid.New(),
		TaskID:      taskID,
		Content:     req.Content,
		CommentType: req.CommentType,
		CreatedAt:   time.Now(),
	}
	if req.AgentID != "" {
		aid, err := uuid.Parse(req.AgentID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent_id"})
			return
		}
		comment.AgentID = &aid
	}

	if err := h.teamStore.AddTaskComment(r.Context(), comment); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": comment.ID})
}

// POST /v1/teams/{teamId}/worker/tasks/{taskId}/complete
func (h *TeamWorkerHandler) handleComplete(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	var req struct {
		Result  string `json:"result"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	slog.Info("worker.complete_task", "task_id", taskID, "agent_id", req.AgentID)

	if err := h.teamStore.CompleteTask(r.Context(), taskID, teamID, req.Result); err != nil {
		if isConflictError(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task not in progress"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	task, err := h.teamStore.GetTask(r.Context(), taskID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

// POST /v1/teams/{teamId}/worker/tasks/{taskId}/fail
func (h *TeamWorkerHandler) handleFail(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}
	taskID, ok := h.parseTaskID(w, r)
	if !ok {
		return
	}

	var req struct {
		Reason  string `json:"reason"`
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	slog.Info("worker.fail_task", "task_id", taskID, "reason", req.Reason)

	if err := h.teamStore.FailTask(r.Context(), taskID, teamID, req.Reason); err != nil {
		if isConflictError(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task not in progress"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /v1/teams/{teamId}/worker/heartbeat
func (h *TeamWorkerHandler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !h.checkStore(w, r) {
		return
	}
	teamID, ok := h.parseTeamID(w, r)
	if !ok {
		return
	}

	var req struct {
		WorkerID      string `json:"worker_id"`
		CurrentTaskID string `json:"current_task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if req.CurrentTaskID != "" {
		taskID, err := uuid.Parse(req.CurrentTaskID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid current_task_id"})
			return
		}
		if err := h.teamStore.RenewTaskLock(r.Context(), taskID, teamID); err != nil {
			slog.Warn("worker.heartbeat_renew_failed", "task_id", taskID, "worker_id", req.WorkerID, "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

// isConflictError checks if a store error indicates a CAS conflict (row not matched).
func isConflictError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no rows") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "conflict") ||
		strings.Contains(msg, "already")
}
