package http

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// WorkerSessionManager tracks active WorkerSession instances by task ID.
// Thread-safe via sync.Map.
type WorkerSessionManager struct {
	sessions sync.Map // uuid.UUID → *WorkerSession
}

func NewWorkerSessionManager() *WorkerSessionManager {
	return &WorkerSessionManager{}
}

// Register adds a WorkerSession for the given task.
func (m *WorkerSessionManager) Register(taskID uuid.UUID, session *WorkerSession) {
	m.sessions.Store(taskID, session)
}

// Unregister removes a WorkerSession for the given task.
func (m *WorkerSessionManager) Unregister(taskID uuid.UUID) {
	m.sessions.Delete(taskID)
}

// Get returns the active WorkerSession for a task, or nil.
func (m *WorkerSessionManager) Get(taskID uuid.UUID) *WorkerSession {
	v, ok := m.sessions.Load(taskID)
	if !ok {
		return nil
	}
	return v.(*WorkerSession)
}

// InjectMessage sends a raw NDJSON message to the worker session for a task.
func (m *WorkerSessionManager) InjectMessage(taskID uuid.UUID, msg []byte) error {
	session := m.Get(taskID)
	if session == nil {
		return fmt.Errorf("no active worker session for task %s", taskID)
	}
	select {
	case session.injectCh <- msg:
		return nil
	default:
		return fmt.Errorf("worker session inject channel full for task %s", taskID)
	}
}

// Interrupt sends an interrupt control_request to the worker session.
func (m *WorkerSessionManager) Interrupt(taskID uuid.UUID) error {
	session := m.Get(taskID)
	if session == nil {
		return fmt.Errorf("no active worker session for task %s", taskID)
	}
	return session.SendInterrupt()
}

// Count returns the number of active sessions.
func (m *WorkerSessionManager) Count() int {
	count := 0
	m.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
