package service

import (
	"fmt"
	"time"

	"github.com/efreitasn/miniexchange/internal/domain"
	"github.com/efreitasn/miniexchange/internal/engine"
	"github.com/efreitasn/miniexchange/internal/store"
)

// PriceResponse represents the response for GET /stocks/{symbol}/price.
type PriceResponse struct {
	Symbol         string
	CurrentPrice   *int64     // nil when no trades ever
	Window         string     // e.g. "5m"
	TradesInWindow int
	LastTradeAt    *time.Time // nil when no trades ever
}

// BookPriceLevel represents an aggregated price level in the book response.
type BookPriceLevel struct {
	Price         int64
	TotalQuantity int64
	OrderCount    int
}

// BookResponse represents the response for GET /stocks/{symbol}/book.
type BookResponse struct {
	Symbol     string
	Bids       []BookPriceLevel
	Asks       []BookPriceLevel
	Spread     *int64 // nil if either side empty
	SnapshotAt time.Time
}

// QuotePriceLevel represents a single price level in the quote response.
type QuotePriceLevel struct {
	Price    int64
	Quantity int64
}

// QuoteResponse represents the response for GET /stocks/{symbol}/quote.
type QuoteResponse struct {
	Symbol            string
	Side              domain.OrderSide
	QuantityRequested int64
	QuantityAvailable int64
	FullyFillable     bool
	EstimatedAvgPrice *int64 // nil when no liquidity
	EstimatedTotal    *int64 // nil when no liquidity
	PriceLevels       []QuotePriceLevel
	QuotedAt          time.Time
}

// StockService handles stock price, book, and quote queries.
type StockService struct {
	tradeStore *store.TradeStore
	books      *engine.BookManager
	matcher    *engine.Matcher
	vwapWindow time.Duration
	symbols    *domain.SymbolRegistry
}

// NewStockService creates a new StockService with the given dependencies.
func NewStockService(
	tradeStore *store.TradeStore,
	books *engine.BookManager,
	matcher *engine.Matcher,
	vwapWindow time.Duration,
	symbols *domain.SymbolRegistry,
) *StockService {
	return &StockService{
		tradeStore: tradeStore,
		books:      books,
		matcher:    matcher,
		vwapWindow: vwapWindow,
		symbols:    symbols,
	}
}

// GetPrice returns the current reference price for a symbol, computed as
// VWAP over the configured time window. Falls back to the last trade's
// price if no trades exist in the window. Returns null price if no trades
// have ever occurred.
func (s *StockService) GetPrice(symbol string) (*PriceResponse, error) {
	if !s.symbols.Exists(symbol) {
		return nil, domain.ErrSymbolNotFound
	}

	trades := s.tradeStore.GetBySymbol(symbol)
	now := time.Now()
	windowStart := now.Add(-s.vwapWindow)

	resp := &PriceResponse{
		Symbol: symbol,
		Window: formatDuration(s.vwapWindow),
	}

	if len(trades) == 0 {
		// No trades ever — return null price.
		return resp, nil
	}

	// Find the last trade's timestamp.
	lastTrade := trades[len(trades)-1]
	resp.LastTradeAt = &lastTrade.ExecutedAt

	// Compute VWAP: iterate backwards from the tail until executed_at
	// falls outside the window.
	var sumPriceQty int64
	var sumQty int64
	var tradesInWindow int

	for i := len(trades) - 1; i >= 0; i-- {
		t := trades[i]
		if t.ExecutedAt.Before(windowStart) {
			break
		}
		sumPriceQty += t.Price * t.Quantity
		sumQty += t.Quantity
		tradesInWindow++
	}

	resp.TradesInWindow = tradesInWindow

	if sumQty > 0 {
		// VWAP = sum(price * quantity) / sum(quantity)
		vwap := sumPriceQty / sumQty
		resp.CurrentPrice = &vwap
	} else {
		// No trades in window — fallback to last trade's price.
		resp.CurrentPrice = &lastTrade.Price
	}

	return resp, nil
}

// GetBook returns the top N price levels of the order book for a symbol.
func (s *StockService) GetBook(symbol string, depth int) (*BookResponse, error) {
	if !s.symbols.Exists(symbol) {
		return nil, domain.ErrSymbolNotFound
	}

	if depth < 1 || depth > 50 {
		return nil, &domain.ValidationError{
			Message: "depth must be between 1 and 50",
		}
	}

	book := s.books.GetOrCreate(symbol)

	book.RLock()
	defer book.RUnlock()

	topBids := book.TopBids(depth)
	topAsks := book.TopAsks(depth)

	bids := make([]BookPriceLevel, len(topBids))
	for i, pl := range topBids {
		bids[i] = BookPriceLevel{
			Price:         pl.Price,
			TotalQuantity: pl.TotalQuantity,
			OrderCount:    pl.OrderCount,
		}
	}

	asks := make([]BookPriceLevel, len(topAsks))
	for i, pl := range topAsks {
		asks[i] = BookPriceLevel{
			Price:         pl.Price,
			TotalQuantity: pl.TotalQuantity,
			OrderCount:    pl.OrderCount,
		}
	}

	resp := &BookResponse{
		Symbol:     symbol,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: time.Now(),
	}

	// Compute spread = best_ask - best_bid (null if either side empty).
	if len(topBids) > 0 && len(topAsks) > 0 {
		spread := topAsks[0].Price - topBids[0].Price
		resp.Spread = &spread
	}

	return resp, nil
}

// GetQuote simulates a market order against the current book and returns
// the estimated result without placing an order.
func (s *StockService) GetQuote(symbol string, side domain.OrderSide, quantity int64) (*QuoteResponse, error) {
	if !s.symbols.Exists(symbol) {
		return nil, domain.ErrSymbolNotFound
	}

	if side != domain.OrderSideBid && side != domain.OrderSideAsk {
		return nil, &domain.ValidationError{
			Message: "side must be 'bid' or 'ask'",
		}
	}

	if quantity <= 0 {
		return nil, &domain.ValidationError{
			Message: "quantity must be a positive integer",
		}
	}

	result := s.matcher.SimulateMarketOrder(symbol, side, quantity)

	priceLevels := make([]QuotePriceLevel, len(result.PriceLevels))
	for i, pl := range result.PriceLevels {
		priceLevels[i] = QuotePriceLevel{
			Price:    pl.Price,
			Quantity: pl.Quantity,
		}
	}

	return &QuoteResponse{
		Symbol:            symbol,
		Side:              side,
		QuantityRequested: quantity,
		QuantityAvailable: result.QuantityAvailable,
		FullyFillable:     result.FullyFillable,
		EstimatedAvgPrice: result.EstimatedAvgPrice,
		EstimatedTotal:    result.EstimatedTotal,
		PriceLevels:       priceLevels,
		QuotedAt:          time.Now(),
	}, nil
}

// formatDuration converts a time.Duration to a human-readable string
// like "5m" for the window field.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	minutes := int(d.Minutes())
	if d == time.Duration(minutes)*time.Minute && minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return d.String()
}
