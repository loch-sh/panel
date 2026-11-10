package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"host-panel-api/internal/model"
)

// JSONFileStore implements UserStore with a JSON file as storage.
// Writes are atomic (write tmp + rename) to prevent corruption.
type JSONFileStore struct {
	mu       sync.RWMutex
	users    map[string]*model.User
	filePath string
	logger   *slog.Logger
}

// NewJSONFileStore loads the existing JSON file or creates an empty store.
func NewJSONFileStore(filePath string, logger *slog.Logger) (*JSONFileStore, error) {
	s := &JSONFileStore{
		users:    make(map[string]*model.User),
		filePath: filePath,
		logger:   logger,
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("users file not found, creating empty store", "path", filePath)
			return s, nil
		}
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, &s.users); err != nil {
		return nil, fmt.Errorf("decoding JSON from %s: %w", filePath, err)
	}

	// Function containers are never granted to a restricted profile: a
	// permission stored before that rule is dropped (and ignored anyway).
	for email, u := range s.users {
		if u != nil && u.DropFunctionPermissions() {
			logger.Warn("users: function container permissions dropped", "email", email)
		}
	}
	logger.Info("users loaded", "count", len(s.users), "path", filePath)
	return s, nil
}

func (s *JSONFileStore) Get(email string) (*model.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	return u, ok
}

func (s *JSONFileStore) Set(email string, user *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[email] = user
	s.persist()
}

func (s *JSONFileStore) Delete(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; !ok {
		return false
	}
	delete(s.users, email)
	s.persist()
	return true
}

func (s *JSONFileStore) List() []*model.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	return users
}

func (s *JSONFileStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// GetOrCreate atomically returns an existing user or creates one.
// Returns (user, true) if already existed, (user, false) if created.
func (s *JSONFileStore) GetOrCreate(email string, create func(currentCount int) *model.User) (*model.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[email]; ok {
		return u, true
	}
	u := create(len(s.users))
	s.users[email] = u
	s.persist()
	return u, false
}

// persist writes the current state to disk atomically.
// Must be called with the write lock held.
func (s *JSONFileStore) persist() {
	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		s.logger.Error("failed to serialize users", "error", err)
		return
	}

	dir := filepath.Dir(s.filePath)
	tmp, err := os.CreateTemp(dir, "users-*.json.tmp")
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
