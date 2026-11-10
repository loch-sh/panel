package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"host-panel-api/internal/model"
)

// APIKeyStore defines the API key persistence interface.
type APIKeyStore interface {
	Create(key *model.APIKey)
	List(email string) []*model.APIKey
	GetByHash(hash string) (*model.APIKey, bool)
	Delete(id, email string) bool
	DeleteByEmail(email string)
	UpdateLastUsed(id string)
}

// APIKeyFileStore implements APIKeyStore with a JSON file as storage.
type APIKeyFileStore struct {
	mu       sync.RWMutex
	keys     map[string]*model.APIKey // keyed by ID
	byHash   map[string]string        // keyHash → ID (index for fast lookup)
	filePath string
	logger   *slog.Logger
}

// NewAPIKeyFileStore loads the existing JSON file or creates an empty store.
func NewAPIKeyFileStore(filePath string, logger *slog.Logger) (*APIKeyFileStore, error) {
	s := &APIKeyFileStore{
		keys:     make(map[string]*model.APIKey),
		byHash:   make(map[string]string),
		filePath: filePath,
		logger:   logger,
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("api keys file not found, creating empty store", "path", filePath)
			return s, nil
		}
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, &s.keys); err != nil {
		return nil, fmt.Errorf("decoding JSON from %s: %w", filePath, err)
	}

	// Build the hash index
	for id, k := range s.keys {
		s.byHash[k.KeyHash] = id
	}

	logger.Info("api keys loaded", "count", len(s.keys), "path", filePath)
	return s, nil
}

func (s *APIKeyFileStore) Create(key *model.APIKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.ID] = key
	s.byHash[key.KeyHash] = key.ID
	s.persist()
}

func (s *APIKeyFileStore) List(email string) []*model.APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.APIKey
	for _, k := range s.keys {
		if k.UserEmail == email {
			result = append(result, k)
		}
	}
	return result
}

func (s *APIKeyFileStore) GetByHash(hash string) (*model.APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHash[hash]
	if !ok {
		return nil, false
	}
	key, ok := s.keys[id]
	return key, ok
}

func (s *APIKeyFileStore) Delete(id, email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keys[id]
	if !ok || key.UserEmail != email {
		return false
	}
	delete(s.byHash, key.KeyHash)
	delete(s.keys, id)
	s.persist()
	return true
}

// DeleteByEmail removes all API keys belonging to a given user.
func (s *APIKeyFileStore) DeleteByEmail(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, key := range s.keys {
		if key.UserEmail == email {
			delete(s.byHash, key.KeyHash)
			delete(s.keys, id)
			changed = true
		}
	}
	if changed {
		s.persist()
	}
}

func (s *APIKeyFileStore) UpdateLastUsed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keys[id]
	if !ok {
		return
	}
	now := time.Now()
	key.LastUsedAt = &now
	s.persist()
}

// persist writes the current state to disk atomically.
// Must be called with the write lock held.
func (s *APIKeyFileStore) persist() {
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		s.logger.Error("failed to serialize api keys", "error", err)
		return
	}

	dir := filepath.Dir(s.filePath)
	tmp, err := os.CreateTemp(dir, "apikeys-*.json.tmp")
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
