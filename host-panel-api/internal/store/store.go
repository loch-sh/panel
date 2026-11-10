package store

import "host-panel-api/internal/model"

// UserStore defines the user persistence interface.
type UserStore interface {
	Get(email string) (*model.User, bool)
	Set(email string, user *model.User)
	Delete(email string) bool
	List() []*model.User
	Count() int
	// GetOrCreate atomically returns an existing user or creates one.
	// The create func receives the current user count and is called under
	// the write lock only if the user does not exist.
	GetOrCreate(email string, create func(currentCount int) *model.User) (*model.User, bool)
}
