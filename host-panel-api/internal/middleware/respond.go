package middleware

import (
	"encoding/json"
	"net/http"
)

// writeErrorJSON writes a JSON error response consistent with the handler package format.
// This ensures middleware and handlers return the same Content-Type and structure.
func writeErrorJSON(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is sent: a write error only means the client is gone.
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
}
