package signal

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tonyjoanes/huginn/internal/source"
)

// EarningsDetector detects stocks with recent positive EPS surprises above a threshold.
type EarningsDetector struct {
	provider     source.EarningsProvider
	minSurprise  float64 // e.g. 0.10 = 10%
	lookbackDays int
}

func NewEarningsDetector(p source.EarningsProvider, minSurprisePct float64, lookbackDays int) *EarningsDetector {
	return &EarningsDetector{
		provider:     p,
		minSurprise:  minSurprisePct / 100.0,
		lookbackDays: lookbackDays,
	}
}

func (d *EarningsDetector) Name() string { return "earnings_surprise" }

func (d *EarningsDetector) Detect(ctx context.Context, tickers []string) ([]Signal, error) {
	const maxConcurrent = 5 // FMP has tighter rate limits
	sem := make(chan struct{}, maxConcurrent)

	var mu sync.Mutex
	var results []Signal
	var wg sync.WaitGroup

	cutoff := time.Now().AddDate(0, 0, -d.lookbackDays)

	for _, ticker := range tickers {
		select {
		case <-ctx.Done():
			break
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(t string) {
			defer wg.Done()
			defer func() { <-sem }()

			sig := d.checkTicker(t, cutoff)
			if sig != nil {
				mu.Lock()
				results = append(results, *sig)
				mu.Unlock()
			}
		}(ticker)
	}

	wg.Wait()
	return results, nil
}

func (d *EarningsDetector) checkTicker(ticker string, cutoff time.Time) *Signal {
	data, err := d.provider.FetchEarnings(ticker)
	if err != nil || data == nil {
		return nil
	}

	// Must be reported within lookback window
	if data.ReportedAt.Before(cutoff) {
		return nil
	}

	// Must beat estimate by minSurprise
	if data.Surprise < d.minSurprise {
		return nil
	}

	// Normalised strength: 10% beat at threshold = 0.33, 30% beat = 1.0
	strength := math.Min(data.Surprise/(3*d.minSurprise), 1.0)

	return &Signal{
		Ticker: ticker,
		Type:   "earnings_surprise",
		Description: fmt.Sprintf("EPS beat: actual $%.2f vs est $%.2f (+%.1f%%) reported %s",
			data.ActualEPS, data.EstimatedEPS, data.Surprise*100,
			data.ReportedAt.Format("Jan 2")),
		Strength:   strength,
		DetectedAt: time.Now(),
		Metadata: map[string]interface{}{
			"actual_eps":    data.ActualEPS,
			"estimated_eps": data.EstimatedEPS,
			"surprise_pct":  data.Surprise * 100,
			"reported_at":   data.ReportedAt,
		},
	}
}
