package source

import (
	"time"
)

// Quote represents a single day's OHLCV data for a ticker.
type Quote struct {
	Ticker    string
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    int64
	Date      time.Time
	AvgVolume int64 // computed 20-day average, populated by signal detectors
}

// NewsItem represents a single news article with sentiment.
type NewsItem struct {
	Ticker    string
	Headline  string
	Summary   string
	Source    string
	URL       string
	Sentiment float64 // -1.0 (negative) to 1.0 (positive)
	Published time.Time
}

// InsiderTrade represents a Form 4 insider transaction.
type InsiderTrade struct {
	Ticker          string
	InsiderName     string
	Title           string
	TransactionType string // "P" = purchase, "S" = sale
	Shares          int64
	Price           float64
	Date            time.Time
	IsAutomatic     bool // true if 10b5-1 automatic plan
}

// EarningsData holds the most recent earnings surprise data for a ticker.
type EarningsData struct {
	Ticker       string
	ActualEPS    float64
	EstimatedEPS float64
	Surprise     float64 // as a decimal, e.g. 0.15 = 15%
	ReportedAt   time.Time
}

// Fundamentals holds key financial metrics for a ticker.
type Fundamentals struct {
	Ticker        string
	Sector        string
	MarketCap     float64
	CurrentPrice  float64
	PE            float64
	ForwardPE     float64
	Revenue       float64
	RevenueGrowth float64 // as a percentage, e.g. 12.5 = 12.5%
	EPS           float64
	GrossMargin   float64 // as a percentage
	OpMargin      float64 // as a percentage
	DE            float64 // debt/equity ratio
	FCF           float64
	High52W       float64
	Low52W        float64
}

// Optional provider interfaces — each data source implements only the ones it supports.

// QuoteProvider can fetch historical OHLCV candles.
type QuoteProvider interface {
	FetchCandles(ticker string, days int) ([]Quote, error)
}

// NewsProvider can fetch recent news articles.
type NewsProvider interface {
	FetchNews(ticker string, days int) ([]NewsItem, error)
}

// InsiderProvider can fetch Form 4 insider transactions.
type InsiderProvider interface {
	FetchInsiderTrades(ticker string, days int) ([]InsiderTrade, error)
}

// EarningsProvider can fetch earnings surprise data.
type EarningsProvider interface {
	FetchEarnings(ticker string) (*EarningsData, error)
}

// FundamentalsProvider can fetch company fundamentals.
type FundamentalsProvider interface {
	FetchFundamentals(ticker string) (*Fundamentals, error)
}

// RateLimiter is a simple token-bucket rate limiter for API calls.
type RateLimiter struct {
	tokens chan struct{}
}

// NewRateLimiter creates a rate limiter allowing callsPerSecond API calls per second.
func NewRateLimiter(callsPerSecond int) *RateLimiter {
	rl := &RateLimiter{
		tokens: make(chan struct{}, callsPerSecond),
	}
	// Pre-fill the bucket
	for i := 0; i < callsPerSecond; i++ {
		rl.tokens <- struct{}{}
	}
	// Refill at the given rate
	go func() {
		interval := time.Second / time.Duration(callsPerSecond)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case rl.tokens <- struct{}{}:
			default:
				// bucket full, drop refill token
			}
		}
	}()
	return rl
}

// Wait blocks until a token is available.
func (rl *RateLimiter) Wait() {
	<-rl.tokens
}
