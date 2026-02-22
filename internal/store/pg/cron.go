package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adhocore/gronx"
	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/cron"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const defaultCronCacheTTL = 2 * time.Minute

// PGCronStore implements store.CronStore backed by Postgres.
// GetDueJobs() uses an in-memory cache with TTL to reduce DB polling (1s interval).
type PGCronStore struct {
	db      *sql.DB
	mu      sync.Mutex
	onJob   func(job *store.CronJob) (string, error)
	running bool
	stop    chan struct{}

	// Job cache: reduces GetDueJobs polling from 86,400 queries/day to ~720/day
	jobCache    []store.CronJob
	cacheLoaded bool
	cacheTime   time.Time
	cacheTTL    time.Duration

	retryCfg cron.RetryConfig
}

func NewPGCronStore(db *sql.DB) *PGCronStore {
	return &PGCronStore{db: db, cacheTTL: defaultCronCacheTTL, retryCfg: cron.DefaultRetryConfig()}
}

// SetRetryConfig overrides the default retry configuration.
func (s *PGCronStore) SetRetryConfig(cfg cron.RetryConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retryCfg = cfg
}

func (s *PGCronStore) AddJob(name string, schedule store.CronSchedule, message string, deliver bool, channel, to, agentID string) (*store.CronJob, error) {
	payload := store.CronPayload{
		Kind: "agent_turn", Message: message, Deliver: deliver, Channel: channel, To: to,
	}
	payloadJSON, _ := json.Marshal(payload)

	id := uuid.Must(uuid.NewV7())
	now := time.Now()
	scheduleKind := schedule.Kind
	deleteAfterRun := schedule.Kind == "at"

	var cronExpr, tz *string
	var runAt *time.Time
	if schedule.Expr != "" {
		cronExpr = &schedule.Expr
	}
	if schedule.AtMS != nil {
		t := time.UnixMilli(*schedule.AtMS)
		runAt = &t
	}
	if schedule.TZ != "" {
		tz = &schedule.TZ
	}

	var agentUUID *uuid.UUID
	if agentID != "" {
		aid, err := uuid.Parse(agentID)
		if err == nil {
			agentUUID = &aid
		}
	}

	nextRun := computeNextRun(&schedule, now)

	_, err := s.db.Exec(
		`INSERT INTO cron_jobs (id, agent_id, name, enabled, schedule_kind, cron_expression, run_at, timezone,
		 payload, delete_after_run, next_run_at, created_at, updated_at)
		 VALUES ($1, $2, $3, true, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, agentUUID, name, scheduleKind, cronExpr, runAt, tz,
		payloadJSON, deleteAfterRun, nextRun, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create cron job: %w", err)
	}

	s.cacheLoaded = false // invalidate cache

	job, _ := s.GetJob(id.String())
	return job, nil
}

func (s *PGCronStore) GetJob(jobID string) (*store.CronJob, bool) {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return nil, false
	}
	job, err := s.scanJob(id)
	if err != nil {
		return nil, false
	}
	return job, true
}

func (s *PGCronStore) ListJobs(includeDisabled bool) []store.CronJob {
	q := `SELECT id, agent_id, name, enabled, schedule_kind, cron_expression, run_at, timezone,
		 payload, delete_after_run, next_run_at, last_run_at, last_status, last_error,
		 created_at, updated_at FROM cron_jobs`
	if !includeDisabled {
		q += " WHERE enabled = true"
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.db.Query(q)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []store.CronJob
	for rows.Next() {
		job, err := scanCronRow(rows)
		if err != nil {
			continue
		}
		result = append(result, *job)
	}
	return result
}

func (s *PGCronStore) RemoveJob(jobID string) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job ID: %s", jobID)
	}
	_, err = s.db.Exec("DELETE FROM cron_jobs WHERE id = $1", id)
	if err != nil {
		return err
	}
	s.cacheLoaded = false
	return nil
}

func (s *PGCronStore) UpdateJob(jobID string, patch store.CronJobPatch) (*store.CronJob, error) {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("invalid job ID: %s", jobID)
	}

	updates := make(map[string]interface{})
	if patch.Name != "" {
		updates["name"] = patch.Name
	}
	if patch.Enabled != nil {
		updates["enabled"] = *patch.Enabled
	}
	if patch.Schedule != nil {
		updates["schedule_kind"] = patch.Schedule.Kind
		if patch.Schedule.Expr != "" {
			updates["cron_expression"] = patch.Schedule.Expr
		}
	}
	if patch.DeleteAfterRun != nil {
		updates["delete_after_run"] = *patch.DeleteAfterRun
	}

	// Update agent_id column
	if patch.AgentID != nil {
		if *patch.AgentID == "" {
			updates["agent_id"] = nil
		} else if aid, parseErr := uuid.Parse(*patch.AgentID); parseErr == nil {
			updates["agent_id"] = aid
		}
	}

	// Update payload JSONB — fetch current, merge patch fields, re-serialize
	needsPayloadUpdate := patch.Message != "" || patch.Deliver != nil || patch.Channel != nil || patch.To != nil
	if needsPayloadUpdate {
		var payloadJSON []byte
		if scanErr := s.db.QueryRow("SELECT payload FROM cron_jobs WHERE id = $1", id).Scan(&payloadJSON); scanErr == nil {
			var payload store.CronPayload
			json.Unmarshal(payloadJSON, &payload)

			if patch.Message != "" {
				payload.Message = patch.Message
			}
			if patch.Deliver != nil {
				payload.Deliver = *patch.Deliver
			}
			if patch.Channel != nil {
				payload.Channel = *patch.Channel
			}
			if patch.To != nil {
				payload.To = *patch.To
			}

			merged, _ := json.Marshal(payload)
			updates["payload"] = merged
		}
	}

	updates["updated_at"] = time.Now()

	if err := execMapUpdate(context.Background(), s.db, "cron_jobs", id, updates); err != nil {
		return nil, err
	}

	s.cacheLoaded = false
	job, _ := s.scanJob(id)
	return job, nil
}

func (s *PGCronStore) EnableJob(jobID string, enabled bool) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job ID: %s", jobID)
	}
	_, err = s.db.Exec("UPDATE cron_jobs SET enabled = $1, updated_at = $2 WHERE id = $3", enabled, time.Now(), id)
	if err != nil {
		return err
	}
	s.cacheLoaded = false
	return nil
}

func (s *PGCronStore) GetRunLog(jobID string, limit int) []store.CronRunLogEntry {
	if limit <= 0 {
		limit = 20
	}

	var rows *sql.Rows
	var err error
	if jobID != "" {
		id, parseErr := uuid.Parse(jobID)
		if parseErr != nil {
			return nil
		}
		rows, err = s.db.Query(
			"SELECT job_id, status, error, summary, ran_at FROM cron_run_logs WHERE job_id = $1 ORDER BY ran_at DESC LIMIT $2",
			id, limit)
	} else {
		rows, err = s.db.Query(
			"SELECT job_id, status, error, summary, ran_at FROM cron_run_logs ORDER BY ran_at DESC LIMIT $1", limit)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var result []store.CronRunLogEntry
	for rows.Next() {
		var jobUUID uuid.UUID
		var status string
		var errStr, summary *string
		var ranAt time.Time
		if err := rows.Scan(&jobUUID, &status, &errStr, &summary, &ranAt); err != nil {
			continue
		}
		result = append(result, store.CronRunLogEntry{
			Ts:      ranAt.UnixMilli(),
			JobID:   jobUUID.String(),
			Status:  status,
			Error:   derefStr(errStr),
			Summary: derefStr(summary),
		})
	}
	return result
}

func (s *PGCronStore) Status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	s.db.QueryRow("SELECT COUNT(*) FROM cron_jobs WHERE enabled = true").Scan(&count)
	return map[string]interface{}{
		"enabled": s.running,
		"jobs":    count,
	}
}

func (s *PGCronStore) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	s.stop = make(chan struct{})
	s.running = true
	go s.runLoop()
	slog.Info("pg cron service started")
	return nil
}

func (s *PGCronStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	close(s.stop)
	s.running = false
}

func (s *PGCronStore) SetOnJob(handler func(job *store.CronJob) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onJob = handler
}

func (s *PGCronStore) RunJob(jobID string, force bool) (bool, string, error) {
	job, ok := s.GetJob(jobID)
	if !ok {
		return false, "", fmt.Errorf("job %s not found", jobID)
	}

	s.mu.Lock()
	handler := s.onJob
	s.mu.Unlock()

	if handler == nil {
		return false, "", fmt.Errorf("no job handler configured")
	}

	result, err := handler(job)
	return true, result, err
}

func (s *PGCronStore) GetDueJobs(now time.Time) []store.CronJob {
	s.mu.Lock()
	if !s.cacheLoaded || time.Since(s.cacheTime) > s.cacheTTL {
		s.refreshJobCache()
	}
	jobs := s.jobCache
	s.mu.Unlock()

	var due []store.CronJob
	for i := range jobs {
		if jobs[i].Enabled && jobs[i].State.NextRunAtMS != nil {
			nextRun := time.UnixMilli(*jobs[i].State.NextRunAtMS)
			if !nextRun.After(now) {
				due = append(due, jobs[i])
			}
		}
	}
	return due
}

// refreshJobCache reloads all enabled jobs from DB. Must be called with mu held.
func (s *PGCronStore) refreshJobCache() {
	rows, err := s.db.Query(
		`SELECT id, agent_id, name, enabled, schedule_kind, cron_expression, run_at, timezone,
		 payload, delete_after_run, next_run_at, last_run_at, last_status, last_error,
		 created_at, updated_at FROM cron_jobs WHERE enabled = true`)
	if err != nil {
		return
	}
	defer rows.Close()

	s.jobCache = nil
	for rows.Next() {
		job, err := scanCronRow(rows)
		if err != nil {
			continue
		}
		s.jobCache = append(s.jobCache, *job)
	}
	s.cacheLoaded = true
	s.cacheTime = time.Now()
}

// InvalidateCache forces a cache refresh on the next GetDueJobs call.
func (s *PGCronStore) InvalidateCache() {
	s.mu.Lock()
	s.cacheLoaded = false
	s.mu.Unlock()
}

// --- Internal ---

func (s *PGCronStore) scanJob(id uuid.UUID) (*store.CronJob, error) {
	row := s.db.QueryRow(
		`SELECT id, agent_id, name, enabled, schedule_kind, cron_expression, run_at, timezone,
		 payload, delete_after_run, next_run_at, last_run_at, last_status, last_error,
		 created_at, updated_at FROM cron_jobs WHERE id = $1`, id)
	return scanCronSingleRow(row)
}

func (s *PGCronStore) runLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.checkAndRunDueJobs()
		}
	}
}

func (s *PGCronStore) checkAndRunDueJobs() {
	dueJobs := s.GetDueJobs(time.Now())
	if len(dueJobs) == 0 {
		return
	}

	s.mu.Lock()
	handler := s.onJob
	s.mu.Unlock()

	if handler == nil {
		return
	}

	for _, job := range dueJobs {
		// Clear next_run to prevent duplicate
		if id, parseErr := uuid.Parse(job.ID); parseErr == nil {
			s.db.Exec("UPDATE cron_jobs SET next_run_at = NULL WHERE id = $1", id)
		}

		jobCopy := job
		result, attempts, err := cron.ExecuteWithRetry(func() (string, error) {
			return handler(&jobCopy)
		}, s.retryCfg)

		if attempts > 1 {
			slog.Info("cron job retried", "id", job.ID, "attempts", attempts, "success", err == nil)
		}

		now := time.Now()
		status := "ok"
		var lastError *string
		if err != nil {
			status = "error"
			errStr := err.Error()
			lastError = &errStr
		}

		// Log run
		logID := uuid.Must(uuid.NewV7())
		var summary *string
		if err == nil {
			s := cron.TruncateOutput(result)
			summary = &s
		}
		if id, parseErr := uuid.Parse(job.ID); parseErr == nil {
			s.db.Exec(
				`INSERT INTO cron_run_logs (id, job_id, status, error, summary, ran_at)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				logID, id, status, lastError, summary, now,
			)
		}

		// Recompute next run or delete
		if job.DeleteAfterRun {
			if id, parseErr := uuid.Parse(job.ID); parseErr == nil {
				s.db.Exec("DELETE FROM cron_jobs WHERE id = $1", id)
			}
		} else if id, parseErr := uuid.Parse(job.ID); parseErr == nil {
			schedule := job.Schedule
			next := computeNextRun(&schedule, now)
			s.db.Exec(
				"UPDATE cron_jobs SET last_run_at = $1, last_status = $2, last_error = $3, next_run_at = $4, updated_at = $5 WHERE id = $6",
				now, status, lastError, next, now, id,
			)
		}
	}

	// Invalidate cache after job execution changed next_run_at values
	s.mu.Lock()
	s.cacheLoaded = false
	s.mu.Unlock()
}

// --- Scan helpers ---

type cronRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanCronRow(row cronRowScanner) (*store.CronJob, error) {
	var id uuid.UUID
	var agentID *uuid.UUID
	var name, scheduleKind string
	var enabled, deleteAfterRun bool
	var cronExpr, tz, lastStatus, lastError *string
	var runAt, nextRunAt, lastRunAt *time.Time
	var payloadJSON []byte
	var createdAt, updatedAt time.Time

	err := row.Scan(&id, &agentID, &name, &enabled, &scheduleKind, &cronExpr, &runAt, &tz,
		&payloadJSON, &deleteAfterRun, &nextRunAt, &lastRunAt, &lastStatus, &lastError,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	var payload store.CronPayload
	json.Unmarshal(payloadJSON, &payload)

	job := &store.CronJob{
		ID:      id.String(),
		Name:    name,
		Enabled: enabled,
		Schedule: store.CronSchedule{
			Kind: scheduleKind,
		},
		Payload:        payload,
		CreatedAtMS:    createdAt.UnixMilli(),
		UpdatedAtMS:    updatedAt.UnixMilli(),
		DeleteAfterRun: deleteAfterRun,
	}

	if agentID != nil {
		job.AgentID = agentID.String()
	}
	if cronExpr != nil {
		job.Schedule.Expr = *cronExpr
	}
	if runAt != nil {
		ms := runAt.UnixMilli()
		job.Schedule.AtMS = &ms
	}
	if tz != nil {
		job.Schedule.TZ = *tz
	}
	if nextRunAt != nil {
		ms := nextRunAt.UnixMilli()
		job.State.NextRunAtMS = &ms
	}
	if lastRunAt != nil {
		ms := lastRunAt.UnixMilli()
		job.State.LastRunAtMS = &ms
	}
	if lastStatus != nil {
		job.State.LastStatus = *lastStatus
	}
	if lastError != nil {
		job.State.LastError = *lastError
	}

	return job, nil
}

func scanCronSingleRow(row *sql.Row) (*store.CronJob, error) {
	return scanCronRow(row)
}

// --- Helpers ---

func computeNextRun(schedule *store.CronSchedule, now time.Time) *time.Time {
	switch schedule.Kind {
	case "at":
		if schedule.AtMS != nil {
			t := time.UnixMilli(*schedule.AtMS)
			if t.After(now) {
				return &t
			}
		}
		return nil
	case "every":
		if schedule.EveryMS != nil && *schedule.EveryMS > 0 {
			t := now.Add(time.Duration(*schedule.EveryMS) * time.Millisecond)
			return &t
		}
		return nil
	case "cron":
		if schedule.Expr == "" {
			return nil
		}
		nextTime, err := gronx.NextTickAfter(schedule.Expr, now, false)
		if err != nil {
			return nil
		}
		return &nextTime
	default:
		return nil
	}
}
