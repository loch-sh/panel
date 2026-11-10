package cron

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

// Scheduler manages the registration and execution of cron jobs.
// Each job is protected against concurrent execution via a lock map.
type Scheduler struct {
	mu      sync.Mutex
	cron    *robfigcron.Cron
	entries map[string]robfigcron.EntryID // job ID → cron entry ID
	running map[string]bool               // job ID → currently running
	// warnNoFunctions logs once that function_invoke jobs are not
	// scheduled (functions module disabled).
	warnNoFunctions sync.Once
	store           *Store
	executor        *Executor
	logger          *slog.Logger
}

// NewScheduler creates a Scheduler without starting it.
func NewScheduler(store *Store, executor *Executor, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		cron: robfigcron.New(
			robfigcron.WithParser(robfigcron.NewParser(
				robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow | robfigcron.Descriptor,
			)),
		),
		entries:  make(map[string]robfigcron.EntryID),
		running:  make(map[string]bool),
		store:    store,
		executor: executor,
		logger:   logger,
	}
}

// Start loads all enabled jobs from the store and starts the scheduler.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := s.store.ListEnabled()
	for _, job := range jobs {
		// A function deleted while the panel was stopped (or by hand):
		// its jobs are disabled as at a deletion.
		if job.Type == JobTypeFunctionInvoke && job.FunctionInvoke != nil && s.executor.functionGone(job.FunctionInvoke.Function) {
			s.store.Update(job.ID, func(j *Job) {
				j.Enabled = false
				j.DisabledReason = DisabledReasonFunctionDeleted
				j.UpdatedAt = time.Now()
			})
			s.logger.Warn("cron job disabled: its function no longer exists", "id", job.ID, "name", job.Name, "function", job.FunctionInvoke.Function)
			continue
		}
		if err := s.addJobLocked(job); err != nil {
			s.logger.Error("failed to register cron job",
				"id", job.ID,
				"name", job.Name,
				"error", err,
			)
		}
	}

	s.cron.Start()
	s.logger.Info("cron scheduler started", "jobs", len(s.entries))
}

// Stop gracefully stops the scheduler, waiting for running jobs to complete.
func (s *Scheduler) Stop() {
	s.logger.Info("stopping cron scheduler")
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.logger.Info("cron scheduler stopped")
}

// AddJob registers a new job in the scheduler (if enabled).
func (s *Scheduler) AddJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !job.Enabled {
		return nil
	}
	return s.addJobLocked(job)
}

// UpdateJob removes the old registration and re-registers the job if enabled.
func (s *Scheduler) UpdateJob(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeJobLocked(job.ID)

	if !job.Enabled {
		return nil
	}
	return s.addJobLocked(job)
}

// RemoveJob unregisters a job from the scheduler.
func (s *Scheduler) RemoveJob(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeJobLocked(id)
}

// RunNow triggers immediate execution of a job in a goroutine.
// Concurrent execution locking still applies.
func (s *Scheduler) RunNow(ctx context.Context, job *Job) {
	go s.executeWithLock(job.ID)
}

// addJobLocked adds a job to the cron instance. The caller must hold s.mu.
func (s *Scheduler) addJobLocked(job *Job) error {
	// Without the functions module a function_invoke job would only fail
	// (and be audited) at every tick: it is not scheduled. It is again
	// when the panel restarts with the module.
	if job.Type == JobTypeFunctionInvoke && !s.executor.HasFunctions() {
		s.warnNoFunctions.Do(func() {
			s.logger.Warn("functions module disabled: function_invoke cron jobs are not scheduled")
		})
		return nil
	}
	jobID := job.ID
	entryID, err := s.cron.AddFunc(job.Schedule, func() {
		s.executeWithLock(jobID)
	})
	if err != nil {
		return fmt.Errorf("adding schedule %q: %w", job.Schedule, err)
	}

	s.entries[job.ID] = entryID
	s.logger.Debug("cron job registered",
		"id", job.ID,
		"name", job.Name,
		"schedule", job.Schedule,
	)
	return nil
}

// removeJobLocked removes a job from the cron instance. The caller must hold s.mu.
func (s *Scheduler) removeJobLocked(id string) {
	if entryID, ok := s.entries[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, id)
		s.logger.Debug("cron job removed", "id", id)
	}
}

// executeWithLock executes a job with concurrent execution protection.
func (s *Scheduler) executeWithLock(jobID string) {
	// Check and acquire the execution lock
	s.mu.Lock()
	if s.running[jobID] {
		s.mu.Unlock()
		s.logger.Warn("cron job already running, execution skipped", "id", jobID)
		now := time.Now()
		s.store.Update(jobID, func(j *Job) { j.LastSkipped = &now })
		return
	}
	s.running[jobID] = true
	s.mu.Unlock()

	// Ensure the lock is released at the end
	defer func() {
		s.mu.Lock()
		delete(s.running, jobID)
		s.mu.Unlock()
	}()

	// Retrieve the latest version of the job from the store
	job, ok := s.store.Get(jobID)
	if !ok || !job.Enabled {
		return
	}

	// Mark the job as "running" in the store
	now := time.Now()
	job.LastRun = &now
	job.LastStatus = JobStatusRunning
	job.LastError = ""
	job.LastOutput = ""
	s.store.Update(job.ID, func(j *Job) {
		j.LastRun, j.LastStatus, j.LastError, j.LastOutput = job.LastRun, job.LastStatus, "", ""
	})

	// Execute the job (updates LastStatus/LastError/LastOutput in the store)
	s.executor.Run(context.Background(), job)

	// If one-time execution, disable the job and remove it from the scheduler
	// Decided on the live job: it may have been changed while it ran.
	disabled := false
	s.store.Update(job.ID, func(j *Job) {
		if j.Once && j.Enabled {
			j.Enabled = false
			disabled = true
		}
	})
	if disabled {
		s.mu.Lock()
		s.removeJobLocked(job.ID)
		s.mu.Unlock()
		s.logger.Info("one-time cron job disabled", "id", job.ID, "name", job.Name)
	}
}

// DisableFunctionJobs disables the function_invoke jobs of a function (it
// was deleted) and records why; the jobs are kept, so that they can be
// pointed at another function or deleted. It returns the jobs it changed.
// A run in progress ends normally (its function is gone: it fails).
func (s *Scheduler) DisableFunctionJobs(function, reason string) []*Job {
	var changed []*Job
	for _, job := range s.store.List() {
		if job.Type != JobTypeFunctionInvoke || job.FunctionInvoke == nil || job.FunctionInvoke.Function != function {
			continue
		}
		var updated Job
		matched := false
		s.store.Update(job.ID, func(j *Job) {
			// Changed since the listing: left alone.
			if j.Type != JobTypeFunctionInvoke || j.FunctionInvoke == nil || j.FunctionInvoke.Function != function {
				return
			}
			matched = true
			j.Enabled = false
			j.DisabledReason = reason
			j.UpdatedAt = time.Now()
			updated = *j
		})
		if !matched {
			continue
		}
		s.RemoveJob(job.ID)
		changed = append(changed, &updated)
	}
	return changed
}

// IsRunning reports whether a job is currently running.
func (s *Scheduler) IsRunning(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[jobID]
}

// GenerateID creates a cryptographically secure random hexadecimal identifier.
func GenerateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
