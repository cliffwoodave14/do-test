package api

import (
	"encoding/json"
	"net/http"
)

// errorBody is the consistent shape of every error response.
type errorBody struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	// Encoding errors here mean the response is already partially written;
	// there is nothing useful to send to the client, so we drop it.
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends a structured error response with a single message.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// writeErrorDetails sends a structured error with field-level details.
func writeErrorDetails(w http.ResponseWriter, status int, msg string, details []string) {
	writeJSON(w, status, errorBody{Error: msg, Details: details})
}
