package handler

import (
	"sync"
	"time"

	"host-panel-api/internal/model"
)

// mockAPIKeyStore is an in-memory implementation of store.APIKeyStore for testing.
type mockAPIKeyStore struct {
	mu     sync.RWMutex
	keys   map[string]*model.APIKey
	byHash map[string]string
}

func newMockAPIKeyStore() *mockAPIKeyStore {
	return &mockAPIKeyStore{
		keys:   make(map[string]*model.APIKey),
		byHash: make(map[string]string),
	}
}

func (s *mockAPIKeyStore) Create(key *model.APIKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key.ID] = key
	s.byHash[key.KeyHash] = key.ID
}

func (s *mockAPIKeyStore) List(email string) []*model.APIKey {
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

func (s *mockAPIKeyStore) GetByHash(hash string) (*model.APIKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHash[hash]
	if !ok {
		return nil, false
	}
	key, ok := s.keys[id]
	return key, ok
}

func (s *mockAPIKeyStore) Delete(id, email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keys[id]
	if !ok || key.UserEmail != email {
		return false
	}
	delete(s.byHash, key.KeyHash)
	delete(s.keys, id)
	return true
}

func (s *mockAPIKeyStore) DeleteByEmail(email string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, key := range s.keys {
		if key.UserEmail == email {
			delete(s.byHash, key.KeyHash)
			delete(s.keys, id)
		}
	}
}

func (s *mockAPIKeyStore) UpdateLastUsed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key, ok := s.keys[id]; ok {
		now := time.Now()
		key.LastUsedAt = &now
	}
}

// mockUserStore is an in-memory implementation of store.UserStore for testing.
type mockUserStore struct {
	mu    sync.RWMutex
	users map[string]*model.User
}

func newMockUserStore() *mockUserStore {
	return &mockUserStore{
		users: make(map[string]*model.User),
	}
}

func (s *mockUserStore) Get(email string) (*model.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[email]
	return u, ok
}

func (s *mockUserStore) Set(email string, user *model.User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[email] = user
}

func (s *mockUserStore) Delete(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[email]; !ok {
		return false
	}
	delete(s.users, email)
	return true
}

func (s *mockUserStore) List() []*model.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	users := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	return users
}

func (s *mockUserStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

func (s *mockUserStore) GetOrCreate(email string, create func(currentCount int) *model.User) (*model.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[email]; ok {
		return u, true
	}
	u := create(len(s.users))
	s.users[email] = u
	return u, false
}
