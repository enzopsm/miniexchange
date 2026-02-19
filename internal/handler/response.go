package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// WriteJSON writes a JSON response with the given status code and data.
// Sets Content-Type to application/json before writing the status code.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data) // Write error intentionally ignored in response helper
}

// errorResponse is the standard error response format.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteError writes a standard error response with the given status code,
// error code, and human-readable message.
func WriteError(w http.ResponseWriter, status int, errorCode, message string) {
	WriteJSON(w, status, errorResponse{
		Error:   errorCode,
		Message: message,
	})
}

// ParseJSON decodes the request body as JSON into v.
// It validates that the Content-Type header is application/json and
// returns an error for missing/incorrect content type or malformed JSON.
func ParseJSON(r *http.Request, v any) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("Request body must be valid JSON with Content-Type: application/json")
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("Request body must be valid JSON with Content-Type: application/json")
	}

	return nil
}
