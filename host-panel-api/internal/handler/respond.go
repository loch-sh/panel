package handler

import (
	"encoding/json"
	"net/http"
)

// writeJSON serializes v as JSON and writes it to the response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError returns a standardized JSON error with a machine-readable code.
// The "code" field is used by the frontend for localized messages.
// The "error" field is a human-readable fallback (used by the CLI and for debugging).
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}

// decodeStrict reads a small JSON body (at most maxBytes), refusing unknown
// fields and trailing data. It writes the 400 response itself and returns
// false on failure.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, ErrInvalidRequestBody, "Invalid request body")
		return false
	}
	return true
}
