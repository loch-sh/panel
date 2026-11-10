package cron

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists cron jobs in a JSON file.
// Writes are atomic (write tmp + rename) to prevent corruption.
type Store struct {
	mu       sync.RWMutex
	jobs     map[string]*Job
	filePath string
	logger   *slog.Logger
}

// NewStore loads the existing JSON file or creates an empty store.
func NewStore(filePath string, logger *slog.Logger) (*Store, error) {
	s := &Store{
		jobs:     make(map[string]*Job),
		filePath: filePath,
		logger:   logger,
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("cron file not found, creating empty store", "path", filePath)
			return s, nil
		}
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, &s.jobs); err != nil {
		return nil, fmt.Errorf("JSON decoding of %s: %w", filePath, err)
	}

	logger.Info("cron jobs loaded", "count", len(s.jobs), "path", filePath)
	return s, nil
}

// Get returns a copy of the job by ID.
// The copy prevents data races when the executor modifies a running job.
func (s *Store) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	dup := *j
	return &dup, true
}

// Set creates or updates a job.
func (s *Store) Set(id string, job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = job
	s.persist()
}

// Update applies fn to the stored job under the write lock and persists
// it. It returns false (and does nothing) if the job no longer exists: a
// run ending after the job was deleted must not bring it back.
func (s *Store) Update(id string, fn func(*Job)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	dup := *j
	fn(&dup)
	s.jobs[id] = &dup
	s.persist()
	return true
}

// Delete removes a job. Returns false if not found.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return false
	}
	delete(s.jobs, id)
	s.persist()
	return true
}

// List returns copies of all jobs sorted by creation date in descending order.
func (s *Store) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		dup := *j
		jobs = append(jobs, &dup)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}

// ListEnabled returns copies of enabled jobs only.
func (s *Store) ListEnabled() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]*Job, 0)
	for _, j := range s.jobs {
		if j.Enabled {
			dup := *j
			jobs = append(jobs, &dup)
		}
	}
	return jobs
}

// persist writes the current state to disk atomically.
// Must be called with the write lock held.
func (s *Store) persist() {
	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		s.logger.Error("failed to serialize cron jobs", "error", err)
		return
	}

	dir := filepath.Dir(s.filePath)
	tmp, err := os.CreateTemp(dir, "cron-*.json.tmp")
	if err != nil {
		s.logger.Error("failed to create temporary file", "error", err)
		return
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		s.logger.Error("failed to write temporary file", "error", err)
		return
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		s.logger.Error("failed to close temporary file", "error", err)
		return
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		os.Remove(tmpPath)
		s.logger.Error("failed to perform atomic rename", "error", err)
		return
	}
}
