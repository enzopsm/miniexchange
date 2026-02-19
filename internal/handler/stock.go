package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/service"
	"github.com/go-chi/chi/v5"
)

// StockHandler handles HTTP requests for stock endpoints.
type StockHandler struct {
	stockSvc *service.StockService
}

// NewStockHandler creates a new StockHandler.
func NewStockHandler(stockSvc *service.StockService) *StockHandler {
	return &StockHandler{stockSvc: stockSvc}
}

// priceResponse is the JSON response for GET /stocks/{symbol}/price.
type priceResponse struct {
	Symbol       string   `json:"symbol"`
	CurrentPrice *float64 `json:"current_price"`
	Window       string   `json:"window"`
	TradesInWin  int      `json:"trades_in_window"`
	LastTradeAt  *string  `json:"last_trade_at"`
}

// bookLevelResponse is a single price level in the book response.
type bookLevelResponse struct {
	Price         float64 `json:"price"`
	TotalQuantity int64   `json:"total_quantity"`
	OrderCount    int     `json:"order_count"`
}

// bookResponse is the JSON response for GET /stocks/{symbol}/book.
type bookResponse struct {
	Symbol     string              `json:"symbol"`
	Bids       []bookLevelResponse `json:"bids"`
	Asks       []bookLevelResponse `json:"asks"`
	Spread     *float64            `json:"spread"`
	SnapshotAt string              `json:"snapshot_at"`
}

// quoteLevelResponse is a single price level in the quote response.
type quoteLevelResponse struct {
	Price    float64 `json:"price"`
	Quantity int64   `json:"quantity"`
}

// quoteResponse is the JSON response for GET /stocks/{symbol}/quote.
type quoteResponse struct {
	Symbol            string               `json:"symbol"`
	Side              string               `json:"side"`
	QuantityRequested int64                `json:"quantity_requested"`
	QuantityAvailable int64                `json:"quantity_available"`
	FullyFillable     bool                 `json:"fully_fillable"`
	EstimatedAvgPrice *float64             `json:"estimated_average_price"`
	EstimatedTotal    *float64             `json:"estimated_total"`
	PriceLevels       []quoteLevelResponse `json:"price_levels"`
	QuotedAt          string               `json:"quoted_at"`
}

// GetPrice handles GET /stocks/{symbol}/price.
func (h *StockHandler) GetPrice(w http.ResponseWriter, r *http.Request) {
	symbol := chi.URLParam(r, "symbol")

	price, err := h.stockSvc.GetPrice(symbol)
	if err != nil {
		mapStockError(w, err)
		return
	}

	resp := priceResponse{
		Symbol:      price.Symbol,
		Window:      price.Window,
		TradesInWin: price.TradesInWindow,
	}

	if price.CurrentPrice != nil {
		v := domain.CentsToDollars(*price.CurrentPrice)
		resp.CurrentPrice = &v
	}
	if price.LastTradeAt != nil {
		s := price.LastTradeAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.LastTradeAt = &s
	}

	WriteJSON(w, http.StatusOK, resp)
}

// GetBook handles GET /stocks/{symbol}/book.
func (h *StockHandler) GetBook(w http.ResponseWriter, r *http.Request) {
	symbol := chi.URLParam(r, "symbol")

	// Parse depth query param (default 10, max 50).
	depth := 10
	if d := r.URL.Query().Get("depth"); d != "" {
		var err error
		depth, err = strconv.Atoi(d)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "validation_error", "depth must be a valid integer")
			return
		}
	}

	book, err := h.stockSvc.GetBook(symbol, depth)
	if err != nil {
		mapStockError(w, err)
		return
	}

	bids := make([]bookLevelResponse, len(book.Bids))
	for i, b := range book.Bids {
		bids[i] = bookLevelResponse{
			Price:         domain.CentsToDollars(b.Price),
			TotalQuantity: b.TotalQuantity,
			OrderCount:    b.OrderCount,
		}
	}

	asks := make([]bookLevelResponse, len(book.Asks))
	for i, a := range book.Asks {
		asks[i] = bookLevelResponse{
			Price:         domain.CentsToDollars(a.Price),
			TotalQuantity: a.TotalQuantity,
			OrderCount:    a.OrderCount,
		}
	}

	resp := bookResponse{
		Symbol:     book.Symbol,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: book.SnapshotAt.UTC().Format("2006-01-02T15:04:05Z"),
	}

	if book.Spread != nil {
		v := domain.CentsToDollars(*book.Spread)
		resp.Spread = &v
	}

	WriteJSON(w, http.StatusOK, resp)
}

// GetQuote handles GET /stocks/{symbol}/quote.
func (h *StockHandler) GetQuote(w http.ResponseWriter, r *http.Request) {
	symbol := chi.URLParam(r, "symbol")

	side := r.URL.Query().Get("side")
	quantityStr := r.URL.Query().Get("quantity")

	// Parse quantity.
	quantity, err := strconv.ParseInt(quantityStr, 10, 64)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", "quantity must be a positive integer")
		return
	}

	quote, err := h.stockSvc.GetQuote(symbol, domain.OrderSide(side), quantity)
	if err != nil {
		mapStockError(w, err)
		return
	}

	priceLevels := make([]quoteLevelResponse, len(quote.PriceLevels))
	for i, pl := range quote.PriceLevels {
		priceLevels[i] = quoteLevelResponse{
			Price:    domain.CentsToDollars(pl.Price),
			Quantity: pl.Quantity,
		}
	}

	resp := quoteResponse{
		Symbol:            quote.Symbol,
		Side:              string(quote.Side),
		QuantityRequested: quote.QuantityRequested,
		QuantityAvailable: quote.QuantityAvailable,
		FullyFillable:     quote.FullyFillable,
		PriceLevels:       priceLevels,
		QuotedAt:          quote.QuotedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}

	if quote.EstimatedAvgPrice != nil {
		v := domain.CentsToDollars(*quote.EstimatedAvgPrice)
		resp.EstimatedAvgPrice = &v
	}
	if quote.EstimatedTotal != nil {
		v := domain.CentsToDollars(*quote.EstimatedTotal)
		resp.EstimatedTotal = &v
	}

	WriteJSON(w, http.StatusOK, resp)
}

// mapStockError maps domain errors to HTTP responses for stock endpoints.
func mapStockError(w http.ResponseWriter, err error) {
	var validationErr *domain.ValidationError
	if errors.As(err, &validationErr) {
		WriteError(w, http.StatusBadRequest, "validation_error", validationErr.Message)
		return
	}

	switch {
	case errors.Is(err, domain.ErrSymbolNotFound):
		WriteError(w, http.StatusNotFound, "symbol_not_found", err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
}
