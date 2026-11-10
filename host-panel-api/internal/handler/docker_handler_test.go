package handler

import (
	"testing"

	"host-panel-api/internal/model"

	"github.com/moby/moby/api/types/container"
)

func TestUserCanAccessContainer_Admin(t *testing.T) {
	user := &model.User{Email: "admin@test.com", Profile: model.ProfileAdmin}
	if !userCanAccessContainer(user, "/my-container") {
		t.Error("admin should always have access")
	}
}

func TestUserCanAccessContainer_RestrictedWithPermission(t *testing.T) {
	user := &model.User{
		Email:       "user@test.com",
		Profile:     model.ProfileRestricted,
		Permissions: []string{"my-container", "other-container"},
	}
	if !userCanAccessContainer(user, "/my-container") {
		t.Error("restricted user with matching permission should have access")
	}
}

func TestUserCanAccessContainer_RestrictedWithoutPermission(t *testing.T) {
	user := &model.User{
		Email:       "user@test.com",
		Profile:     model.ProfileRestricted,
		Permissions: []string{"other-container"},
	}
	if userCanAccessContainer(user, "/my-container") {
		t.Error("restricted user without matching permission should not have access")
	}
}

func TestUserCanAccessContainer_RestrictedNoPermissions(t *testing.T) {
	user := &model.User{
		Email:   "user@test.com",
		Profile: model.ProfileRestricted,
	}
	if userCanAccessContainer(user, "/any-container") {
		t.Error("restricted user with no permissions should not have access")
	}
}

func TestUserCanAccessContainer_SlashPrefix(t *testing.T) {
	user := &model.User{
		Email:       "user@test.com",
		Profile:     model.ProfileRestricted,
		Permissions: []string{"web-app"},
	}
	// Docker names have leading slash
	if !userCanAccessContainer(user, "/web-app") {
		t.Error("should strip leading slash and match")
	}
}

func TestFilterNonSystem(t *testing.T) {
	containers := []container.Summary{
		{ID: "1", Names: []string{"/app"}, Labels: map[string]string{}},
		{ID: "2", Names: []string{"/traefik"}, Labels: map[string]string{"system": "true"}},
		{ID: "3", Names: []string{"/db"}, Labels: map[string]string{"env": "prod"}},
	}

	result := filterNonSystem(containers)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
	for _, c := range result {
		if _, isSystem := c.Labels["system"]; isSystem {
			t.Errorf("container %s should have been filtered out", c.ID)
		}
	}
}

func TestFilterNonSystem_AllSystem(t *testing.T) {
	containers := []container.Summary{
		{ID: "1", Labels: map[string]string{"system": "true"}},
	}
	result := filterNonSystem(containers)
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestFilterNonSystem_Empty(t *testing.T) {
	result := filterNonSystem(nil)
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestFilterByPermissions(t *testing.T) {
	containers := []container.Summary{
		{ID: "1", Names: []string{"/app-web"}},
		{ID: "2", Names: []string{"/app-db"}},
		{ID: "3", Names: []string{"/monitoring"}},
	}

	permissions := []string{"app-web", "app-db"}
	result := filterByPermissions(containers, permissions)

	if len(result) != 2 {
		t.Fatalf("len = %d, want 2", len(result))
	}
}

func TestFilterByPermissions_NoMatch(t *testing.T) {
	containers := []container.Summary{
		{ID: "1", Names: []string{"/secret-container"}},
	}

	result := filterByPermissions(containers, []string{"other"})
	if len(result) != 0 {
		t.Errorf("len = %d, want 0", len(result))
	}
}

func TestFilterByPermissions_EmptyPermissions(t *testing.T) {
	containers := []container.Summary{
		{ID: "1", Names: []string{"/app"}},
	}

	result := filterByPermissions(containers, nil)
	if len(result) != 0 {
		t.Errorf("len = %d, want 0 with empty permissions", len(result))
	}
}
