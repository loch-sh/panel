package hosting

// Asynchronous operations: registration, background execution, and recovery
// of operations interrupted by an API restart.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

func newOperationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (op *Operation) snapshot() *Operation {
	c := *op
	return &c
}

// startOp registers a running operation, or fails if one is in progress.
func (m *Manager) startOp(name, kind string) (*Operation, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if cur, ok := m.ops[name]; ok && cur.State == OpRunning {
		return nil, &Error{Code: CodeOperationRunning, Message: "an operation is already in progress on this stack"}
	}
	op := &Operation{ID: newOperationID(), Kind: kind, State: OpRunning, StartedAt: time.Now().UTC()}
	m.ops[name] = op
	return op, nil
}

func (m *Manager) dropOp(name string, op *Operation) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.ops[name] == op {
		delete(m.ops, name)
	}
}

// Operation returns a copy of the last operation of a stack, if any.
func (m *Manager) Operation(name string) *Operation {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if op, ok := m.ops[name]; ok {
		return op.snapshot()
	}
	return nil
}

// run executes fn in the background, detached from the request context.
// While it runs, an operation marker in the stack directory lets a restarted
// API report the operation as interrupted (see recoverInterrupted).
func (m *Manager) run(name string, op *Operation, actor string, fn func(ctx context.Context, op *Operation) error) {
	dir := m.stackDir(name)
	if err := writeOperationMarker(dir, op); err != nil {
		m.logger.Warn("hosting: cannot write the operation marker", "stack", name, "error", err)
	}
	go func() {
		m.sem <- struct{}{}
		defer func() { <-m.sem }()

		ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
		defer cancel()

		// fn works on a private copy; readers only see published snapshots.
		work := op.snapshot()
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error("hosting operation panicked", "stack", name, "kind", op.Kind, "panic", r, "stack_trace", string(debug.Stack()))
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return fn(ctx, work)
		}()
		now := time.Now().UTC()
		work.FinishedAt = &now
		work.State = OpSucceeded
		if err != nil {
			work.State = OpFailed
			work.Error = OperationFailedCode(op.Kind)
			m.logger.Error("hosting operation failed", "stack", name, "kind", op.Kind, "error", err, "output", work.Output)
		}
		removeOperationMarker(dir)

		m.opMu.Lock()
		if m.ops[name] == op {
			*op = *work
		}
		m.opMu.Unlock()

		m.onChange()
		m.audit.Info("hosting operation finished", "user", actor, "stack", name, "kind", op.Kind, "state", work.State, "rolled_back", work.RolledBack)
	}()
}

// OperationFailedCode is the error code of a failed operation of a kind,
// e.g. HOSTING_UPDATE_FAILED.
func OperationFailedCode(kind string) string {
	return "HOSTING_" + strings.ToUpper(kind) + "_FAILED"
}

// dropOpLater forgets the operation of a deleted stack shortly after it
// finished, so that pollers still see it complete but it is not kept forever.
func (m *Manager) dropOpLater(name string) {
	m.opMu.Lock()
	op := m.ops[name]
	m.opMu.Unlock()
	time.AfterFunc(time.Minute, func() { m.dropOp(name, op) })
}

// recoverInterrupted reports the operations that were running when the API
// stopped: their marker is still in the stack directory.
func (m *Manager) recoverInterrupted() {
	stacks, err := m.loadAll()
	if err != nil {
		m.logger.Warn("hosting: cannot scan stacks for interrupted operations", "error", err)
		return
	}
	for name, s := range stacks {
		marker, ok := readOperationMarker(s.dir)
		if !ok {
			continue
		}
		now := time.Now().UTC()
		op := &Operation{
			ID: marker.ID, Kind: marker.Kind, State: OpFailed, Error: CodeOperationInterrupted,
			StartedAt: marker.StartedAt, FinishedAt: &now,
			Output: "The API stopped while this operation was running. The files on disk may not match " +
				"the running containers: check the hosting, then update or redeploy it.",
		}
		m.ops[name] = op
		removeOperationMarker(s.dir)
		m.logger.Warn("hosting: operation interrupted by an API restart", "stack", name, "kind", marker.Kind)
	}
}
