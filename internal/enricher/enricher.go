package enricher

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/tonyjoanes/huginn/internal/signal"
	"github.com/tonyjoanes/huginn/internal/source"
)

// EnrichedData holds all context gathered for a signal candidate.
type EnrichedData struct {
	Signal       signal.Signal
	Fundamentals *source.Fundamentals
	Candles      []source.Quote  // last 20 trading days
	News         []source.NewsItem
	Insiders     []source.InsiderTrade
}

// AvgVolume computes the 20-day average volume from candles (excluding the last day).
func (e *EnrichedData) AvgVolume() int64 {
	if len(e.Candles) < 2 {
		return 0
	}
	historical := e.Candles[:len(e.Candles)-1]
	var total int64
	for _, c := range historical {
		total += c.Volume
	}
	return total / int64(len(historical))
}

// TodayVolume returns the volume for the most recent candle.
func (e *EnrichedData) TodayVolume() int64 {
	if len(e.Candles) == 0 {
		return 0
	}
	return e.Candles[len(e.Candles)-1].Volume
}

// Enricher gathers fundamentals, candles, news, and insider data for a signal.
type Enricher struct {
	quotes       source.QuoteProvider
	news         source.NewsProvider
	fundamentals source.FundamentalsProvider
	insiders     source.InsiderProvider
}

// New creates an Enricher. Any provider may be nil; that data source is skipped.
func New(
	quotes source.QuoteProvider,
	news source.NewsProvider,
	fundamentals source.FundamentalsProvider,
	insiders source.InsiderProvider,
) *Enricher {
	return &Enricher{
		quotes:       quotes,
		news:         news,
		fundamentals: fundamentals,
		insiders:     insiders,
	}
}

// Enrich fetches all available data for the given signal concurrently.
// Partial failures are logged and tolerated — we proceed with whatever data we got.
func (e *Enricher) Enrich(ctx context.Context, sig signal.Signal) (*EnrichedData, error) {
	result := &EnrichedData{Signal: sig}

	var wg sync.WaitGroup
	type fetchErr struct {
		name string
		err  error
	}
	errs := make(chan fetchErr, 4)

	if e.quotes != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candles, err := e.quotes.FetchCandles(sig.Ticker, 21)
			if err != nil {
				errs <- fetchErr{"candles", err}
				return
			}
			result.Candles = candles
		}()
	}

	if e.news != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			news, err := e.news.FetchNews(sig.Ticker, 14)
			if err != nil {
				errs <- fetchErr{"news", err}
				return
			}
			// Limit to 10 most recent
			if len(news) > 10 {
				news = news[:10]
			}
			result.News = news
		}()
	}

	if e.fundamentals != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fund, err := e.fundamentals.FetchFundamentals(sig.Ticker)
			if err != nil {
				errs <- fetchErr{"fundamentals", err}
				return
			}
			result.Fundamentals = fund
		}()
	}

	if e.insiders != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			trades, err := e.insiders.FetchInsiderTrades(sig.Ticker, 90)
			if err != nil {
				errs <- fetchErr{"insiders", err}
				return
			}
			result.Insiders = trades
		}()
	}

	wg.Wait()
	close(errs)

	for fe := range errs {
		log.Printf("warn: enrich %s %s: %v", sig.Ticker, fe.name, fe.err)
	}

	// Merge sector from fundamentals into signal metadata if available
	if result.Fundamentals != nil && result.Fundamentals.Sector == "" {
		if meta, ok := sig.Metadata["sector"].(string); ok {
			result.Fundamentals.Sector = meta
		}
	}

	_ = fmt.Sprintf // silence import
	return result, nil
}
