package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/efreitasn/miniexchange/internal/store"
)

// testEnv bundles all dependencies for handler integration tests.
type testEnv struct {
	router     http.Handler
	brokerSvc  *service.BrokerService
	orderSvc   *service.OrderService
	stockSvc   *service.StockService
	webhookSvc *service.WebhookService
}

func newTestEnv() *testEnv {
	bs := store.NewBrokerStore()
	os := store.NewOrderStore()
	ts := store.NewTradeStore()
	ws := store.NewWebhookStore()
	sr := domain.NewSymbolRegistry()
	bm := engine.NewBookManager()
	m := engine.NewMatcher(bm, bs, os, ts, sr)
	e := engine.NewExpiryManager(time.Hour, bm, os, bs, nil) // long interval, no auto-expiry in tests

	webhookSvc := service.NewWebhookService(ws, bs, 5*time.Second)
	brokerSvc := service.NewBrokerService(bs, sr)
	orderSvc := service.NewOrderService(m, e, bs, os, ts, webhookSvc, sr)
	stockSvc := service.NewStockService(ts, bm, m, 5*time.Minute, sr)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(brokerSvc, orderSvc, stockSvc, webhookSvc, logger)

	return &testEnv{
		router:     router,
		brokerSvc:  brokerSvc,
		orderSvc:   orderSvc,
		stockSvc:   stockSvc,
		webhookSvc: webhookSvc,
	}
}

// doJSON sends a JSON request and returns the recorder.
func (env *testEnv) doJSON(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	return rr
}

// doRaw sends a raw request with optional content-type override.
func (env *testEnv) doRaw(t *testing.T, method, path, contentType, rawBody string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	return rr
}

// decodeJSON decodes the response body into v.
func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rr.Body.String())
	}
}

// futureRFC3339 returns a future timestamp string.
func futureRFC3339() string {
	return time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
}

// registerBroker is a helper that registers a broker via the API.
func (env *testEnv) registerBroker(t *testing.T, id string, cash float64, holdings []map[string]any) {
	t.Helper()
	body := map[string]any{
		"broker_id":    id,
		"initial_cash": cash,
	}
	if holdings != nil {
		body["initial_holdings"] = holdings
	}
	rr := env.doJSON(t, "POST", "/brokers", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register broker %s: expected 201, got %d: %s", id, rr.Code, rr.Body.String())
	}
}

// submitLimitOrder is a helper that submits a limit order via the API and returns the response.
func (env *testEnv) submitLimitOrder(t *testing.T, brokerID, side, symbol string, price float64, qty int64) map[string]any {
	t.Helper()
	body := map[string]any{
		"type":            "limit",
		"broker_id":       brokerID,
		"document_number": "DOC1",
		"side":            side,
		"symbol":          symbol,
		"price":           price,
		"quantity":        qty,
		"expires_at":      futureRFC3339(),
	}
	rr := env.doJSON(t, "POST", "/orders", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("submit limit order: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	return resp
}

// --- Healthz ---

func TestHealthz(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "GET", "/healthz", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	decodeJSON(t, rr, &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", resp["status"])
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %s", ct)
	}
}

// --- Broker Endpoints ---

func TestBroker_Register_Success(t *testing.T) {
	env := newTestEnv()
	body := map[string]any{
		"broker_id":    "broker1",
		"initial_cash": 1000.50,
		"initial_holdings": []map[string]any{
			{"symbol": "AAPL", "quantity": 100},
		},
	}
	rr := env.doJSON(t, "POST", "/brokers", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rr, &resp)

	// Verify snake_case fields and decimal monetary values.
	if resp["broker_id"] != "broker1" {
		t.Fatalf("expected broker_id=broker1, got %v", resp["broker_id"])
	}
	if resp["cash_balance"] != 1000.5 {
		t.Fatalf("expected cash_balance=1000.5, got %v", resp["cash_balance"])
	}
	// Verify created_at is RFC 3339.
	createdAt, ok := resp["created_at"].(string)
	if !ok {
		t.Fatal("created_at should be a string")
	}
	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		t.Fatalf("created_at not RFC 3339: %v", err)
	}
}

func TestBroker_Register_Duplicate(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "broker1", 1000, nil)

	body := map[string]any{
		"broker_id":    "broker1",
		"initial_cash": 500,
	}
	rr := env.doJSON(t, "POST", "/brokers", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["error"] != "broker_already_exists" {
		t.Fatalf("expected error=broker_already_exists, got %v", resp["error"])
	}
}

func TestBroker_Register_ValidationErrors(t *testing.T) {
	env := newTestEnv()

	tests := []struct {
		name string
		body map[string]any
	}{
		{"empty broker_id", map[string]any{"broker_id": "", "initial_cash": 100}},
		{"negative cash", map[string]any{"broker_id": "b1", "initial_cash": -1}},
		{"too many decimals", map[string]any{"broker_id": "b1", "initial_cash": 1.999}},
		{"invalid symbol in holdings", map[string]any{
			"broker_id":        "b1",
			"initial_cash":     100,
			"initial_holdings": []map[string]any{{"symbol": "bad", "quantity": 10}},
		}},
		{"zero quantity in holdings", map[string]any{
			"broker_id":        "b1",
			"initial_cash":     100,
			"initial_holdings": []map[string]any{{"symbol": "AAPL", "quantity": 0}},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.doJSON(t, "POST", "/brokers", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestBroker_GetBalance_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "broker1", 5000, []map[string]any{
		{"symbol": "AAPL", "quantity": 50},
	})

	rr := env.doJSON(t, "GET", "/brokers/broker1/balance", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["broker_id"] != "broker1" {
		t.Fatalf("expected broker_id=broker1, got %v", resp["broker_id"])
	}
	if resp["cash_balance"] != 5000.0 {
		t.Fatalf("expected cash_balance=5000, got %v", resp["cash_balance"])
	}
}

func TestBroker_GetBalance_NotFound(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "GET", "/brokers/nonexistent/balance", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// --- Order Endpoints ---

func TestOrder_SubmitLimitBid_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "buyer", 10000, nil)

	body := map[string]any{
		"type":            "limit",
		"broker_id":       "buyer",
		"document_number": "DOC001",
		"side":            "bid",
		"symbol":          "AAPL",
		"price":           150.00,
		"quantity":        10,
		"expires_at":      futureRFC3339(),
	}
	rr := env.doJSON(t, "POST", "/orders", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["type"] != "limit" {
		t.Fatalf("expected type=limit, got %v", resp["type"])
	}
	if resp["status"] != "pending" {
		t.Fatalf("expected status=pending, got %v", resp["status"])
	}
	if resp["price"] != 150.0 {
		t.Fatalf("expected price=150, got %v", resp["price"])
	}
	// Limit orders should include expires_at.
	if _, ok := resp["expires_at"]; !ok {
		t.Fatal("limit order response should include expires_at")
	}
}

func TestOrder_SubmitMarketBid_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 20000, nil)

	// Place an ask so there's liquidity.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 150.00, 10)

	body := map[string]any{
		"type":            "market",
		"broker_id":       "buyer",
		"document_number": "MKT001",
		"side":            "bid",
		"symbol":          "AAPL",
		"quantity":        5,
	}
	rr := env.doJSON(t, "POST", "/orders", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["type"] != "market" {
		t.Fatalf("expected type=market, got %v", resp["type"])
	}
	if resp["status"] != "filled" {
		t.Fatalf("expected status=filled, got %v", resp["status"])
	}
	// Market orders should NOT include price or expires_at.
	if _, ok := resp["price"]; ok {
		t.Fatal("market order response should not include price")
	}
	if _, ok := resp["expires_at"]; ok {
		t.Fatal("market order response should not include expires_at")
	}
}

func TestOrder_Submit_ValidationErrors(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 10000, nil)

	tests := []struct {
		name string
		body map[string]any
	}{
		{"invalid type", map[string]any{
			"type": "invalid", "broker_id": "b1", "document_number": "D1",
			"side": "bid", "symbol": "AAPL", "price": 100.0, "quantity": 1,
			"expires_at": futureRFC3339(),
		}},
		{"missing price for limit", map[string]any{
			"type": "limit", "broker_id": "b1", "document_number": "D1",
			"side": "bid", "symbol": "AAPL", "quantity": 1,
			"expires_at": futureRFC3339(),
		}},
		{"zero quantity", map[string]any{
			"type": "limit", "broker_id": "b1", "document_number": "D1",
			"side": "bid", "symbol": "AAPL", "price": 100.0, "quantity": 0,
			"expires_at": futureRFC3339(),
		}},
		{"market with price", map[string]any{
			"type": "market", "broker_id": "b1", "document_number": "D1",
			"side": "bid", "symbol": "AAPL", "price": 100.0, "quantity": 1,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.doJSON(t, "POST", "/orders", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestOrder_Submit_BrokerNotFound(t *testing.T) {
	env := newTestEnv()
	body := map[string]any{
		"type":            "limit",
		"broker_id":       "nonexistent",
		"document_number": "D1",
		"side":            "bid",
		"symbol":          "AAPL",
		"price":           100.0,
		"quantity":        1,
		"expires_at":      futureRFC3339(),
	}
	rr := env.doJSON(t, "POST", "/orders", body)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestOrder_Submit_InsufficientBalance(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "poor", 10, nil)

	body := map[string]any{
		"type":            "limit",
		"broker_id":       "poor",
		"document_number": "D1",
		"side":            "bid",
		"symbol":          "AAPL",
		"price":           100.0,
		"quantity":        10,
		"expires_at":      futureRFC3339(),
	}
	rr := env.doJSON(t, "POST", "/orders", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["error"] != "insufficient_balance" {
		t.Fatalf("expected error=insufficient_balance, got %v", resp["error"])
	}
}

func TestOrder_Get_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 10000, nil)
	order := env.submitLimitOrder(t, "b1", "bid", "AAPL", 100.0, 5)
	orderID := order["order_id"].(string)

	rr := env.doJSON(t, "GET", "/orders/"+orderID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["order_id"] != orderID {
		t.Fatalf("expected order_id=%s, got %v", orderID, resp["order_id"])
	}
}

func TestOrder_Get_NotFound(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "GET", "/orders/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestOrder_Cancel_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 10000, nil)
	order := env.submitLimitOrder(t, "b1", "bid", "AAPL", 100.0, 5)
	orderID := order["order_id"].(string)

	rr := env.doJSON(t, "DELETE", "/orders/"+orderID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["status"] != "cancelled" {
		t.Fatalf("expected status=cancelled, got %v", resp["status"])
	}
}

func TestOrder_Cancel_NotCancellable(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 20000, nil)

	// Create a fully filled order via matching.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 100.0, 5)
	order := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 100.0, 5)
	orderID := order["order_id"].(string)

	rr := env.doJSON(t, "DELETE", "/orders/"+orderID, nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["error"] != "order_not_cancellable" {
		t.Fatalf("expected error=order_not_cancellable, got %v", resp["error"])
	}
}

func TestOrder_Cancel_NotFound(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "DELETE", "/orders/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// --- Stock Endpoints ---

func TestStock_GetPrice_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Create a trade so there's a price.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 150.0, 10)
	env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 10)

	rr := env.doJSON(t, "GET", "/stocks/AAPL/price", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["symbol"] != "AAPL" {
		t.Fatalf("expected symbol=AAPL, got %v", resp["symbol"])
	}
	if resp["current_price"] != 150.0 {
		t.Fatalf("expected current_price=150, got %v", resp["current_price"])
	}
}

func TestStock_GetPrice_NotFound(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "GET", "/stocks/UNKNOWN/price", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestStock_GetBook_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 50000, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})

	env.submitLimitOrder(t, "b1", "bid", "AAPL", 148.0, 10)
	env.submitLimitOrder(t, "b1", "ask", "AAPL", 152.0, 5)

	rr := env.doJSON(t, "GET", "/stocks/AAPL/book", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["symbol"] != "AAPL" {
		t.Fatalf("expected symbol=AAPL, got %v", resp["symbol"])
	}
	// Spread should be 152 - 148 = 4.
	if resp["spread"] != 4.0 {
		t.Fatalf("expected spread=4, got %v", resp["spread"])
	}
}

func TestStock_GetBook_InvalidDepth(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 10000, nil)
	env.submitLimitOrder(t, "b1", "bid", "AAPL", 100.0, 1)

	rr := env.doJSON(t, "GET", "/stocks/AAPL/book?depth=0", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}

	rr = env.doJSON(t, "GET", "/stocks/AAPL/book?depth=51", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for depth=51, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStock_GetQuote_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 150.0, 50)

	rr := env.doJSON(t, "GET", "/stocks/AAPL/quote?side=bid&quantity=10", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	if resp["fully_fillable"] != true {
		t.Fatalf("expected fully_fillable=true, got %v", resp["fully_fillable"])
	}
}

func TestStock_GetQuote_ValidationErrors(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 10000, nil)
	env.submitLimitOrder(t, "b1", "bid", "AAPL", 100.0, 1)

	// Missing quantity.
	rr := env.doJSON(t, "GET", "/stocks/AAPL/quote?side=bid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// --- Webhook Endpoints ---

func TestWebhook_Upsert_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1000, nil)

	body := map[string]any{
		"broker_id": "b1",
		"url":       "https://example.com/hook",
		"events":    []string{"trade.executed"},
	}
	rr := env.doJSON(t, "POST", "/webhooks", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Re-register same → 200.
	rr = env.doJSON(t, "POST", "/webhooks", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on re-register, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhook_List_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1000, nil)

	body := map[string]any{
		"broker_id": "b1",
		"url":       "https://example.com/hook",
		"events":    []string{"trade.executed"},
	}
	env.doJSON(t, "POST", "/webhooks", body)

	rr := env.doJSON(t, "GET", "/webhooks?broker_id=b1", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	webhooks := resp["webhooks"].([]any)
	if len(webhooks) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(webhooks))
	}
}

func TestWebhook_Delete_Success(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1000, nil)

	body := map[string]any{
		"broker_id": "b1",
		"url":       "https://example.com/hook",
		"events":    []string{"trade.executed"},
	}
	rr := env.doJSON(t, "POST", "/webhooks", body)
	var createResp map[string]any
	decodeJSON(t, rr, &createResp)
	webhooks := createResp["webhooks"].([]any)
	whID := webhooks[0].(map[string]any)["webhook_id"].(string)

	rr = env.doJSON(t, "DELETE", "/webhooks/"+whID, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestWebhook_Delete_NotFound(t *testing.T) {
	env := newTestEnv()
	rr := env.doJSON(t, "DELETE", "/webhooks/nonexistent", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// --- Matching Scenarios ---

func TestMatch_SamePrice(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Ask at 150, then bid at 150 → trade at 150.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 150.0, 10)
	resp := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 10)

	if resp["status"] != "filled" {
		t.Fatalf("expected status=filled, got %v", resp["status"])
	}
	trades := resp["trades"].([]any)
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	trade := trades[0].(map[string]any)
	if trade["price"] != 150.0 {
		t.Fatalf("expected trade price=150, got %v", trade["price"])
	}
	if trade["quantity"] != 10.0 {
		t.Fatalf("expected trade quantity=10, got %v", trade["quantity"])
	}
}

func TestMatch_NoMatch(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Ask at 155, bid at 150 → no match, both rest on book.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 155.0, 10)
	resp := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 10)

	if resp["status"] != "pending" {
		t.Fatalf("expected status=pending, got %v", resp["status"])
	}
	trades := resp["trades"].([]any)
	if len(trades) != 0 {
		t.Fatalf("expected 0 trades, got %d", len(trades))
	}
}

func TestMatch_PriceGap(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Ask at 148, bid at 150 → trade at 148 (ask price).
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 148.0, 10)
	resp := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 10)

	if resp["status"] != "filled" {
		t.Fatalf("expected status=filled, got %v", resp["status"])
	}
	trades := resp["trades"].([]any)
	trade := trades[0].(map[string]any)
	if trade["price"] != 148.0 {
		t.Fatalf("expected trade price=148 (ask price), got %v", trade["price"])
	}
}

func TestMatch_PartialFill(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Ask for 50, bid for 100 → 50 filled, 50 remaining.
	env.submitLimitOrder(t, "seller", "ask", "AAPL", 150.0, 50)
	resp := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 100)

	if resp["status"] != "partially_filled" {
		t.Fatalf("expected status=partially_filled, got %v", resp["status"])
	}
	if resp["filled_quantity"] != 50.0 {
		t.Fatalf("expected filled_quantity=50, got %v", resp["filled_quantity"])
	}
	if resp["remaining_quantity"] != 50.0 {
		t.Fatalf("expected remaining_quantity=50, got %v", resp["remaining_quantity"])
	}
}

func TestMatch_ChronologicalPriority(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "seller1", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "seller2", 0, []map[string]any{
		{"symbol": "AAPL", "quantity": 100},
	})
	env.registerBroker(t, "buyer", 50000, nil)

	// Two asks at same price — seller1 first, then seller2.
	ask1 := env.submitLimitOrder(t, "seller1", "ask", "AAPL", 150.0, 10)
	env.submitLimitOrder(t, "seller2", "ask", "AAPL", 150.0, 10)

	// Bid matches the earlier ask first.
	resp := env.submitLimitOrder(t, "buyer", "bid", "AAPL", 150.0, 5)

	trades := resp["trades"].([]any)
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}

	// Verify the first ask was matched by checking the ask1 order is now partially filled.
	ask1ID := ask1["order_id"].(string)
	rr := env.doJSON(t, "GET", "/orders/"+ask1ID, nil)
	var ask1State map[string]any
	decodeJSON(t, rr, &ask1State)
	if ask1State["filled_quantity"] != 5.0 {
		t.Fatalf("expected seller1 ask filled_quantity=5, got %v", ask1State["filled_quantity"])
	}
}

// --- Content-Type Validation ---

func TestContentType_MissingOnPost(t *testing.T) {
	env := newTestEnv()
	rr := env.doRaw(t, "POST", "/brokers", "", `{"broker_id":"b1","initial_cash":100}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing Content-Type, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestContentType_WrongOnPost(t *testing.T) {
	env := newTestEnv()
	rr := env.doRaw(t, "POST", "/brokers", "text/plain", `{"broker_id":"b1","initial_cash":100}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong Content-Type, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- Response Format Validation ---

func TestResponseFormat_SnakeCaseFields(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1000, nil)

	rr := env.doJSON(t, "GET", "/brokers/b1/balance", nil)
	body := rr.Body.String()

	// Verify snake_case field names are present.
	for _, field := range []string{"broker_id", "cash_balance", "reserved_cash", "available_cash"} {
		if !strings.Contains(body, fmt.Sprintf(`"%s"`, field)) {
			t.Fatalf("response missing snake_case field %q: %s", field, body)
		}
	}
	// Verify no camelCase equivalents.
	for _, bad := range []string{"brokerId", "cashBalance", "reservedCash", "availableCash"} {
		if strings.Contains(body, bad) {
			t.Fatalf("response contains camelCase field %q: %s", bad, body)
		}
	}
}

func TestResponseFormat_MonetaryDecimal(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1234.56, nil)

	rr := env.doJSON(t, "GET", "/brokers/b1/balance", nil)
	var resp map[string]any
	decodeJSON(t, rr, &resp)

	// cash_balance should be decimal 1234.56, not cents 123456.
	if resp["cash_balance"] != 1234.56 {
		t.Fatalf("expected cash_balance=1234.56 (decimal), got %v", resp["cash_balance"])
	}
}

func TestResponseFormat_TimestampRFC3339(t *testing.T) {
	env := newTestEnv()
	env.registerBroker(t, "b1", 1000, nil)
	order := env.submitLimitOrder(t, "b1", "bid", "AAPL", 100.0, 1)

	createdAt, ok := order["created_at"].(string)
	if !ok {
		t.Fatal("created_at should be a string")
	}
	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		t.Fatalf("created_at not RFC 3339: %s", createdAt)
	}

	expiresAt, ok := order["expires_at"].(string)
	if !ok {
		t.Fatal("expires_at should be a string")
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		t.Fatalf("expires_at not RFC 3339: %s", expiresAt)
	}
}
