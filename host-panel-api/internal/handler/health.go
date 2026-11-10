package handler

import (
	"net/http"

	"host-panel-api/internal/docker"
)

// HealthHandler handles the healthcheck endpoint.
type HealthHandler struct {
	Docker *docker.Client
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	dockerOK, _ := h.Docker.Ping(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "up",
		"docker":  dockerOK,
	})
}
