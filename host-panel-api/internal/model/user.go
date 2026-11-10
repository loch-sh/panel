package model

import "strings"

// Profile represents a user's access level.
type Profile string

const (
	ProfileAdmin      Profile = "admin"
	ProfileRestricted Profile = "restricted"
)

// User represents a system user.
type User struct {
	Email        string   `json:"email"`
	Name         string   `json:"name"`
	Profile      Profile  `json:"profile"`
	PasswordHash string   `json:"passwordHash,omitempty"`
	Permissions  []string `json:"permissions"`
}

// FunctionContainerPrefix prefixes the containers of the functions module
// (fn-<name>): they are never granted to a restricted profile.
const FunctionContainerPrefix = "fn-"

// IsFunctionContainerName reports whether a container name (with or
// without its leading "/") is a function container's.
func IsFunctionContainerName(name string) bool {
	return strings.HasPrefix(strings.TrimPrefix(name, "/"), FunctionContainerPrefix)
}

// GrantsContainer reports whether the permissions of a restricted user name
// a container. Function containers are never granted, even by a permission
// stored before they were refused.
func (u *User) GrantsContainer(name string) bool {
	name = strings.TrimPrefix(name, "/")
	if IsFunctionContainerName(name) {
		return false
	}
	for _, p := range u.Permissions {
		if p == name {
			return true
		}
	}
	return false
}

// DropFunctionPermissions removes the function containers from stored
// permissions (granted before they were refused). It reports whether any
// was removed.
func (u *User) DropFunctionPermissions() bool {
	kept := u.Permissions[:0:0]
	for _, p := range u.Permissions {
		if !IsFunctionContainerName(p) {
			kept = append(kept, p)
		}
	}
	dropped := len(kept) != len(u.Permissions)
	if dropped {
		u.Permissions = kept
	}
	return dropped
}
