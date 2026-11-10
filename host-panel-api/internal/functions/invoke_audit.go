package functions

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Invocations are audited aggregated per function and per minute, never
// with their content: a cron job or a script must not flood audit.log
// (decision log §20, plan reviews).

// Outcomes of an invocation.
const (
	outcomeOK            = "ok"
	outcomeFunctionError = "function_error"
	outcomeFailed        = "failed"
)

// maxAuditedActors bounds the users listed in one aggregated entry.
const maxAuditedActors = 5

type invocationStats struct {
	start                   time.Time
	count, errors, failures int
	actors                  []string
	actorsTruncated         bool
}

type invocationAudit struct {
	audit *slog.Logger
	now   func() time.Time
	mu    sync.Mutex
	stats map[string]*invocationStats
	// closed: the final flush ran (shutdown); later invocations are
	// written one by one.
	closed bool
}

func newInvocationAudit(audit *slog.Logger, now func() time.Time) *invocationAudit {
	return &invocationAudit{audit: audit, now: now, stats: map[string]*invocationStats{}}
}

// record counts one invocation; an entry whose minute is over is written
// first.
func (a *invocationAudit) record(function, actor, outcome string) {
	now := a.now()
	a.mu.Lock()
	s := a.stats[function]
	if s != nil && now.Truncate(time.Minute).After(s.start) {
		a.write(function, s)
		s = nil
	}
	if s == nil {
		s = &invocationStats{start: now.Truncate(time.Minute)}
		a.stats[function] = s
	}
	s.count++
	switch outcome {
	case outcomeFunctionError:
		s.errors++
	case outcomeFailed:
		s.failures++
	}
	if !slices.Contains(s.actors, actor) {
		if len(s.actors) < maxAuditedActors {
			s.actors = append(s.actors, actor)
		} else {
			s.actorsTruncated = true
		}
	}
	if a.closed {
		a.write(function, s)
	}
	a.mu.Unlock()
}

// close writes every pending entry; invocations recorded afterwards (still
// running when the server stopped waiting) are written at once.
func (a *invocationAudit) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for fn, s := range a.stats {
		a.write(fn, s)
	}
	a.closed = true
}

// write logs an aggregated entry and forgets it. Called with mu held.
func (a *invocationAudit) write(function string, s *invocationStats) {
	users := strings.Join(s.actors, ",")
	if s.actorsTruncated {
		users += ",…"
	}
	a.audit.Info("function invoked", "user", users, "function", function,
		"since", s.start.UTC().Format(time.RFC3339), "count", s.count, "errors", s.errors, "failures", s.failures)
	delete(a.stats, function)
}

// flush writes every pending entry.
func (a *invocationAudit) flush() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for fn, s := range a.stats {
		a.write(fn, s)
	}
}

// flushOld writes the entries whose minute is over.
func (a *invocationAudit) flushOld() {
	minute := a.now().Truncate(time.Minute)
	a.mu.Lock()
	defer a.mu.Unlock()
	for fn, s := range a.stats {
		if minute.After(s.start) {
			a.write(fn, s)
		}
	}
}

func (a *invocationAudit) run(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.flush()
			return
		case <-t.C:
			a.flushOld()
		}
	}
}
