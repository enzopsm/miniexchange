package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/go-chi/chi/v5"
)

// NewRouter creates a chi router with all routes registered, request logging,
// and Content-Type validation middleware.
func NewRouter(
	brokerSvc *service.BrokerService,
	orderSvc *service.OrderService,
	stockSvc *service.StockService,
	webhookSvc *service.WebhookService,
	logger *slog.Logger,
) chi.Router {
	r := chi.NewRouter()

	// Global middleware.
	r.Use(requestLogging(logger))
	r.Use(contentTypeJSON)

	// Create handlers.
	brokerH := NewBrokerHandler(brokerSvc, orderSvc)
	orderH := NewOrderHandler(orderSvc)
	stockH := NewStockHandler(stockSvc)
	webhookH := NewWebhookHandler(webhookSvc)

	// Health check.
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Broker routes.
	r.Post("/brokers", brokerH.Register)
	r.Get("/brokers/{broker_id}/balance", brokerH.GetBalance)
	r.Get("/brokers/{broker_id}/orders", brokerH.ListOrders)

	// Order routes.
	r.Post("/orders", orderH.SubmitOrder)
	r.Get("/orders/{order_id}", orderH.GetOrder)
	r.Delete("/orders/{order_id}", orderH.CancelOrder)

	// Stock routes.
	r.Get("/stocks/{symbol}/price", stockH.GetPrice)
	r.Get("/stocks/{symbol}/book", stockH.GetBook)
	r.Get("/stocks/{symbol}/quote", stockH.GetQuote)

	// Webhook routes.
	r.Post("/webhooks", webhookH.Upsert)
	r.Get("/webhooks", webhookH.List)
	r.Delete("/webhooks/{webhook_id}", webhookH.Delete)

	return r
}

// requestLogging returns middleware that logs each request's method, path,
// status code, and duration using slog.
func requestLogging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)
			logger.Info("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.status),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// contentTypeJSON is middleware that validates Content-Type for POST, PUT, and
// PATCH requests. If the Content-Type header doesn't start with
// "application/json", it returns 400 Bad Request before the handler runs.
func contentTypeJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			ct := r.Header.Get("Content-Type")
			if ct == "" || !strings.HasPrefix(ct, "application/json") {
				WriteError(w, http.StatusBadRequest, "invalid_request",
					"Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
