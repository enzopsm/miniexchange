package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	t.Run("sets content type and status code", func(t *testing.T) {
		w := httptest.NewRecorder()
		data := map[string]string{"status": "ok"}

		WriteJSON(w, http.StatusOK, data)

		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if w.Code != http.StatusOK {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusOK)
		}

		var result map[string]string
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if result["status"] != "ok" {
			t.Errorf("body status = %q, want %q", result["status"], "ok")
		}
	})

	t.Run("writes 201 Created", func(t *testing.T) {
		w := httptest.NewRecorder()
		data := map[string]int{"id": 42}

		WriteJSON(w, http.StatusCreated, data)

		if w.Code != http.StatusCreated {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusCreated)
		}
	})

	t.Run("encodes struct with snake_case tags", func(t *testing.T) {
		type resp struct {
			BrokerID    string  `json:"broker_id"`
			CashBalance float64 `json:"cash_balance"`
		}
		w := httptest.NewRecorder()
		WriteJSON(w, http.StatusOK, resp{BrokerID: "b1", CashBalance: 100.50})

		var raw map[string]any
		if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if raw["broker_id"] != "b1" {
			t.Errorf("broker_id = %v, want %q", raw["broker_id"], "b1")
		}
		if raw["cash_balance"] != 100.50 {
			t.Errorf("cash_balance = %v, want %v", raw["cash_balance"], 100.50)
		}
	})

	t.Run("encodes null fields", func(t *testing.T) {
		type resp struct {
			Price *float64 `json:"price"`
		}
		w := httptest.NewRecorder()
		WriteJSON(w, http.StatusOK, resp{Price: nil})

		var raw map[string]any
		if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if raw["price"] != nil {
			t.Errorf("price = %v, want nil", raw["price"])
		}
	})
}

func TestWriteError(t *testing.T) {
	t.Run("writes standard error format", func(t *testing.T) {
		w := httptest.NewRecorder()

		WriteError(w, http.StatusBadRequest, "invalid_request", "missing required field")

		if w.Code != http.StatusBadRequest {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
		}
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}

		var resp errorResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if resp.Error != "invalid_request" {
			t.Errorf("error = %q, want %q", resp.Error, "invalid_request")
		}
		if resp.Message != "missing required field" {
			t.Errorf("message = %q, want %q", resp.Message, "missing required field")
		}
	})

	t.Run("writes 404 error", func(t *testing.T) {
		w := httptest.NewRecorder()

		WriteError(w, http.StatusNotFound, "broker_not_found", "Broker not found")

		if w.Code != http.StatusNotFound {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusNotFound)
		}

		var resp errorResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode: %v", err)
		}
		if resp.Error != "broker_not_found" {
			t.Errorf("error = %q, want %q", resp.Error, "broker_not_found")
		}
	})

	t.Run("writes 409 conflict", func(t *testing.T) {
		w := httptest.NewRecorder()

		WriteError(w, http.StatusConflict, "broker_already_exists", "Broker already exists")

		if w.Code != http.StatusConflict {
			t.Errorf("status code = %d, want %d", w.Code, http.StatusConflict)
		}
	})
}

func TestParseJSON(t *testing.T) {
	t.Run("decodes valid JSON with correct content type", func(t *testing.T) {
		body := `{"name":"test","value":42}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		var result struct {
			Name  string `json:"name"`
			Value int    `json:"value"`
		}
		if err := ParseJSON(r, &result); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Errorf("name = %q, want %q", result.Name, "test")
		}
		if result.Value != 42 {
			t.Errorf("value = %d, want %d", result.Value, 42)
		}
	})

	t.Run("accepts content type with charset", func(t *testing.T) {
		body := `{"name":"test"}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json; charset=utf-8")

		var result struct {
			Name string `json:"name"`
		}
		if err := ParseJSON(r, &result); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Errorf("name = %q, want %q", result.Name, "test")
		}
	})

	t.Run("rejects missing content type", func(t *testing.T) {
		body := `{"name":"test"}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

		var result struct {
			Name string `json:"name"`
		}
		err := ParseJSON(r, &result)
		if err == nil {
			t.Fatal("expected error for missing Content-Type")
		}
		if !strings.Contains(err.Error(), "Content-Type") {
			t.Errorf("error = %q, should mention Content-Type", err.Error())
		}
	})

	t.Run("rejects wrong content type", func(t *testing.T) {
		body := `{"name":"test"}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "text/plain")

		var result struct {
			Name string `json:"name"`
		}
		err := ParseJSON(r, &result)
		if err == nil {
			t.Fatal("expected error for wrong Content-Type")
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		body := `{invalid json}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		var result struct {
			Name string `json:"name"`
		}
		err := ParseJSON(r, &result)
		if err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		body := `{"name":"test","unknown_field":"value"}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		var result struct {
			Name string `json:"name"`
		}
		err := ParseJSON(r, &result)
		if err == nil {
			t.Fatal("expected error for unknown fields")
		}
	})

	t.Run("rejects empty body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
		r.Header.Set("Content-Type", "application/json")

		var result struct {
			Name string `json:"name"`
		}
		err := ParseJSON(r, &result)
		if err == nil {
			t.Fatal("expected error for empty body")
		}
	})
}
