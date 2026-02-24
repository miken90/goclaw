package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
)

// QueueMode determines how incoming messages are handled when an agent
// is already processing a message for the same session.
type QueueMode string

const (
	// QueueModeQueue is simple FIFO: new messages wait until current finishes.
	QueueModeQueue QueueMode = "queue"

	// QueueModeFollowup queues as a follow-up after the current run completes.
	QueueModeFollowup QueueMode = "followup"

	// QueueModeInterrupt cancels the current run and starts the new message.
	QueueModeInterrupt QueueMode = "interrupt"
)

// DropPolicy determines which messages to drop when the queue is full.
type DropPolicy string

const (
	DropOld DropPolicy = "old" // drop oldest message
	DropNew DropPolicy = "new" // reject incoming message
)

// QueueConfig configures per-session message queuing.
type QueueConfig struct {
	Mode          QueueMode  `json:"mode"`
	Cap           int        `json:"cap"`
	Drop          DropPolicy `json:"drop"`
	DebounceMs    int        `json:"debounce_ms"`
	MaxConcurrent int        `json:"max_concurrent"` // 0 or 1 = serial (default)
}

// DefaultQueueConfig returns sensible defaults.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		Mode:          QueueModeQueue,
		Cap:           10,
		Drop:          DropOld,
		DebounceMs:    800,
		MaxConcurrent: 1,
	}
}

// RunFunc is the callback that executes an agent run.
// The scheduler calls this when it's the request's turn.
type RunFunc func(ctx context.Context, req agent.RunRequest) (*agent.RunResult, error)

// TokenEstimateFunc returns token estimate and context window for a session.
// Used by adaptive throttle to reduce concurrency near the summary threshold.
type TokenEstimateFunc func(sessionKey string) (tokens int, contextWindow int)

// PendingRequest is a queued agent run awaiting execution.
type PendingRequest struct {
	Req      agent.RunRequest
	ResultCh chan RunOutcome
}

// RunOutcome is the result of a scheduled agent run.
type RunOutcome struct {
	Result *agent.RunResult
	Err    error
}

// SessionQueue manages agent runs for a single session key.
// Supports configurable concurrency: 1 (serial) or N (concurrent).
type SessionQueue struct {
	key      string
	config   QueueConfig
	runFn    RunFunc
	laneMgr *LaneManager
	lane     string

	mu            sync.Mutex
	queue         []*PendingRequest
	activeRuns    map[string]context.CancelFunc // runID → cancel
	activeOrder   []string                      // FIFO order of active runIDs
	maxConcurrent int                           // effective limit (from config or per-session override)
	timer         *time.Timer                   // debounce timer
	parentCtx     context.Context               // stored from first Enqueue call

	tokenEstimateFn TokenEstimateFunc // optional: for adaptive throttle
}

// NewSessionQueue creates a queue for a specific session.
func NewSessionQueue(key, lane string, cfg QueueConfig, laneMgr *LaneManager, runFn RunFunc) *SessionQueue {
	maxC := cfg.MaxConcurrent
	if maxC <= 0 {
		maxC = 1
	}
	return &SessionQueue{
		key:           key,
		config:        cfg,
		runFn:         runFn,
		laneMgr:       laneMgr,
		lane:          lane,
		activeRuns:    make(map[string]context.CancelFunc),
		maxConcurrent: maxC,
	}
}

// SetMaxConcurrent overrides the per-session max concurrent runs.
// Typically called from the consumer when it knows the peer kind (group vs DM).
func (sq *SessionQueue) SetMaxConcurrent(n int) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if n <= 0 {
		n = 1
	}
	sq.maxConcurrent = n
}

// effectiveMaxConcurrent returns the current concurrency limit,
// reduced to 1 when near the summary threshold (adaptive throttle).
// Must be called with sq.mu held.
func (sq *SessionQueue) effectiveMaxConcurrent() int {
	max := sq.maxConcurrent
	if max <= 0 {
		max = 1
	}
	if sq.tokenEstimateFn == nil {
		return max
	}
	tokens, contextWindow := sq.tokenEstimateFn(sq.key)
	if contextWindow > 0 && float64(tokens)/float64(contextWindow) >= 0.6 {
		return 1 // near summary threshold → serialize
	}
	return max
}

// hasCapacity returns whether a new run can start.
// Must be called with sq.mu held.
func (sq *SessionQueue) hasCapacity() bool {
	return len(sq.activeRuns) < sq.effectiveMaxConcurrent()
}

// Enqueue adds a request to the session queue.
// If capacity is available, it starts immediately (after debounce).
// Returns a channel that receives the result when the run completes.
func (sq *SessionQueue) Enqueue(ctx context.Context, req agent.RunRequest) <-chan RunOutcome {
	outcome := make(chan RunOutcome, 1)
	pending := &PendingRequest{Req: req, ResultCh: outcome}

	sq.mu.Lock()
	defer sq.mu.Unlock()

	// Store parent context for spawning future runs
	if sq.parentCtx == nil {
		sq.parentCtx = ctx
	}

	switch sq.config.Mode {
	case QueueModeInterrupt:
		// Cancel all active runs
		for runID, cancel := range sq.activeRuns {
			cancel()
			delete(sq.activeRuns, runID)
		}
		sq.activeOrder = nil
		// Clear existing queue and enqueue this one
		sq.drainQueue(RunOutcome{Err: context.Canceled})
		sq.queue = append(sq.queue, pending)
		if sq.hasCapacity() {
			sq.scheduleNext(ctx)
		}

	default: // queue, followup
		if len(sq.queue) >= sq.config.Cap {
			sq.applyDropPolicy(pending)
		} else {
			sq.queue = append(sq.queue, pending)
		}

		if sq.hasCapacity() {
			sq.scheduleNext(ctx)
		}
	}

	return outcome
}

// scheduleNext starts the next queued request(s), applying debounce.
// Must be called with sq.mu held.
func (sq *SessionQueue) scheduleNext(ctx context.Context) {
	if len(sq.queue) == 0 {
		return
	}

	debounce := time.Duration(sq.config.DebounceMs) * time.Millisecond
	if debounce <= 0 {
		sq.startAvailable(ctx)
		return
	}

	// Reset debounce timer: collapses rapid messages
	if sq.timer != nil {
		sq.timer.Stop()
	}
	sq.timer = time.AfterFunc(debounce, func() {
		sq.mu.Lock()
		defer sq.mu.Unlock()
		if sq.hasCapacity() && len(sq.queue) > 0 {
			sq.startAvailable(ctx)
		}
	})
}

// startAvailable starts as many queued requests as capacity allows.
// Must be called with sq.mu held.
func (sq *SessionQueue) startAvailable(ctx context.Context) {
	for sq.hasCapacity() && len(sq.queue) > 0 {
		sq.startOne(ctx)
	}
}

// startOne picks the first queued request and runs it in the lane.
// Must be called with sq.mu held.
func (sq *SessionQueue) startOne(ctx context.Context) {
	if len(sq.queue) == 0 {
		return
	}

	pending := sq.queue[0]
	sq.queue = sq.queue[1:]

	runID := pending.Req.RunID
	runCtx, cancel := context.WithCancel(ctx)
	sq.activeRuns[runID] = cancel
	sq.activeOrder = append(sq.activeOrder, runID)

	lane := sq.laneMgr.Get(sq.lane)
	if lane == nil {
		lane = sq.laneMgr.Get("main")
	}

	if lane == nil {
		// No lane available — run directly
		go sq.executeRun(runCtx, runID, pending)
		return
	}

	err := lane.Submit(ctx, func() {
		sq.executeRun(runCtx, runID, pending)
	})
	if err != nil {
		pending.ResultCh <- RunOutcome{Err: err}
		close(pending.ResultCh)
		// caller already holds sq.mu — clean up
		delete(sq.activeRuns, runID)
		sq.removeFromOrder(runID)
	}
}

// executeRun runs the agent and then starts the next queued message(s) if capacity allows.
func (sq *SessionQueue) executeRun(ctx context.Context, runID string, pending *PendingRequest) {
	result, err := sq.runFn(ctx, pending.Req)
	pending.ResultCh <- RunOutcome{Result: result, Err: err}
	close(pending.ResultCh)

	sq.mu.Lock()
	delete(sq.activeRuns, runID)
	sq.removeFromOrder(runID)

	if sq.hasCapacity() && len(sq.queue) > 0 {
		// Use parentCtx (not the per-run ctx which may be cancelled)
		sq.scheduleNext(sq.parentCtx)
	}
	sq.mu.Unlock()
}

// removeFromOrder removes a runID from the activeOrder slice.
// Must be called with sq.mu held.
func (sq *SessionQueue) removeFromOrder(runID string) {
	for i, id := range sq.activeOrder {
		if id == runID {
			sq.activeOrder = append(sq.activeOrder[:i], sq.activeOrder[i+1:]...)
			return
		}
	}
}

// applyDropPolicy handles a full queue.
// Must be called with sq.mu held.
func (sq *SessionQueue) applyDropPolicy(incoming *PendingRequest) {
	switch sq.config.Drop {
	case DropOld:
		// Drop the oldest queued message
		if len(sq.queue) > 0 {
			old := sq.queue[0]
			old.ResultCh <- RunOutcome{Err: ErrQueueDropped}
			close(old.ResultCh)
			sq.queue = sq.queue[1:]
		}
		sq.queue = append(sq.queue, incoming)

	case DropNew:
		// Reject the incoming message
		incoming.ResultCh <- RunOutcome{Err: ErrQueueFull}
		close(incoming.ResultCh)

	default:
		// Default to drop old
		if len(sq.queue) > 0 {
			old := sq.queue[0]
			old.ResultCh <- RunOutcome{Err: ErrQueueDropped}
			close(old.ResultCh)
			sq.queue = sq.queue[1:]
		}
		sq.queue = append(sq.queue, incoming)
	}
}

// drainQueue cancels all pending requests with the given outcome.
// Must be called with sq.mu held.
func (sq *SessionQueue) drainQueue(outcome RunOutcome) {
	for _, p := range sq.queue {
		p.ResultCh <- outcome
		close(p.ResultCh)
	}
	sq.queue = nil
}

// CancelOne stops the oldest active run (FIFO).
// Does NOT drain the pending queue. Used by /stop command.
// Returns true if an active run was actually cancelled.
func (sq *SessionQueue) CancelOne() bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if len(sq.activeOrder) == 0 {
		return false
	}

	// Cancel the oldest active run
	runID := sq.activeOrder[0]
	if cancel, ok := sq.activeRuns[runID]; ok {
		cancel()
		delete(sq.activeRuns, runID)
		sq.activeOrder = sq.activeOrder[1:]
		return true
	}
	return false
}

// CancelAll stops all active runs and drains all pending requests.
// Used by /stopall command.
// Returns true if any active run was actually cancelled.
func (sq *SessionQueue) CancelAll() bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	cancelled := false
	for runID, cancel := range sq.activeRuns {
		cancel()
		delete(sq.activeRuns, runID)
		cancelled = true
	}
	sq.activeOrder = nil
	sq.drainQueue(RunOutcome{Err: context.Canceled})
	return cancelled
}

// Cancel is an alias for CancelAll (backward compat with /stop command).
func (sq *SessionQueue) Cancel() bool {
	return sq.CancelAll()
}

// IsActive returns whether any run is currently executing.
func (sq *SessionQueue) IsActive() bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.activeRuns) > 0
}

// ActiveCount returns the number of currently executing runs.
func (sq *SessionQueue) ActiveCount() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.activeRuns)
}

// QueueLen returns the number of pending messages.
func (sq *SessionQueue) QueueLen() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.queue)
}

// --- Scheduler ---

// ScheduleOpts provides per-request overrides for the scheduler.
type ScheduleOpts struct {
	MaxConcurrent int // per-session override (0 = use config default)
}

// Scheduler is the top-level coordinator that manages lanes and session queues.
type Scheduler struct {
	lanes           *LaneManager
	sessions        map[string]*SessionQueue
	config          QueueConfig
	runFn           RunFunc
	mu              sync.RWMutex
	tokenEstimateFn TokenEstimateFunc // optional: for adaptive throttle
}

// NewScheduler creates a scheduler with the given lane and queue config.
func NewScheduler(laneConfigs []LaneConfig, queueCfg QueueConfig, runFn RunFunc) *Scheduler {
	if laneConfigs == nil {
		laneConfigs = DefaultLanes()
	}

	return &Scheduler{
		lanes:    NewLaneManager(laneConfigs),
		sessions: make(map[string]*SessionQueue),
		config:   queueCfg,
		runFn:    runFn,
	}
}

// SetTokenEstimateFunc sets the callback used by adaptive throttle.
// Must be called before any Schedule calls.
func (s *Scheduler) SetTokenEstimateFunc(fn TokenEstimateFunc) {
	s.tokenEstimateFn = fn
}

// Schedule submits a run request to the appropriate session queue and lane.
// Returns a channel that receives the result when the run completes.
func (s *Scheduler) Schedule(ctx context.Context, lane string, req agent.RunRequest) <-chan RunOutcome {
	sq := s.getOrCreateSession(req.SessionKey, lane)
	return sq.Enqueue(ctx, req)
}

// ScheduleWithOpts submits a run request with per-session overrides.
func (s *Scheduler) ScheduleWithOpts(ctx context.Context, lane string, req agent.RunRequest, opts ScheduleOpts) <-chan RunOutcome {
	sq := s.getOrCreateSession(req.SessionKey, lane)
	if opts.MaxConcurrent > 0 {
		sq.SetMaxConcurrent(opts.MaxConcurrent)
	}
	return sq.Enqueue(ctx, req)
}

// getOrCreateSession returns or creates a session queue for the given key.
func (s *Scheduler) getOrCreateSession(sessionKey, lane string) *SessionQueue {
	s.mu.RLock()
	sq, ok := s.sessions[sessionKey]
	s.mu.RUnlock()

	if ok {
		return sq
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if sq, ok := s.sessions[sessionKey]; ok {
		return sq
	}

	sq = NewSessionQueue(sessionKey, lane, s.config, s.lanes, s.runFn)
	if s.tokenEstimateFn != nil {
		sq.tokenEstimateFn = s.tokenEstimateFn
	}
	s.sessions[sessionKey] = sq

	slog.Debug("session queue created", "session", sessionKey, "lane", lane)
	return sq
}

// CancelSession cancels all active runs and drains pending queue for a session.
// Returns true if any active run was cancelled.
func (s *Scheduler) CancelSession(sessionKey string) bool {
	s.mu.RLock()
	sq, ok := s.sessions[sessionKey]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return sq.CancelAll()
}

// CancelOneSession cancels the oldest active run for a session.
// Does NOT drain the pending queue. Used by /stop command.
// Returns true if an active run was cancelled.
func (s *Scheduler) CancelOneSession(sessionKey string) bool {
	s.mu.RLock()
	sq, ok := s.sessions[sessionKey]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return sq.CancelOne()
}

// Stop shuts down all lanes and clears session queues.
func (s *Scheduler) Stop() {
	s.lanes.StopAll()
}

// LaneStats returns utilization metrics for all lanes.
func (s *Scheduler) LaneStats() []LaneStats {
	return s.lanes.AllStats()
}

// Lanes returns the underlying lane manager (for direct access if needed).
func (s *Scheduler) Lanes() *LaneManager {
	return s.lanes
}
