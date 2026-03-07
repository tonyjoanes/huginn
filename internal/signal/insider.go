package signal

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tonyjoanes/huginn/internal/source"
)

// InsiderDetector detects clustered insider buying (multiple insiders purchasing
// within the lookback window).
type InsiderDetector struct {
	provider      source.InsiderProvider
	minInsiders   int
	lookbackDays  int
	exclude10b5_1 bool
}

func NewInsiderDetector(p source.InsiderProvider, minInsiders, lookbackDays int, exclude10b5_1 bool) *InsiderDetector {
	return &InsiderDetector{
		provider:      p,
		minInsiders:   minInsiders,
		lookbackDays:  lookbackDays,
		exclude10b5_1: exclude10b5_1,
	}
}

func (d *InsiderDetector) Name() string { return "insider_buying" }

func (d *InsiderDetector) Detect(ctx context.Context, tickers []string) ([]Signal, error) {
	const maxConcurrent = 5
	sem := make(chan struct{}, maxConcurrent)

	var mu sync.Mutex
	var results []Signal
	var wg sync.WaitGroup

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

			sig := d.checkTicker(t)
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

func (d *InsiderDetector) checkTicker(ticker string) *Signal {
	trades, err := d.provider.FetchInsiderTrades(ticker, d.lookbackDays)
	if err != nil || len(trades) == 0 {
		return nil
	}

	// Filter to purchases, excluding automatic plans if configured
	var purchases []source.InsiderTrade
	for _, t := range trades {
		if t.TransactionType != "P" {
			continue
		}
		if d.exclude10b5_1 && t.IsAutomatic {
			continue
		}
		purchases = append(purchases, t)
	}

	// Count unique insiders
	seen := make(map[string]bool)
	for _, p := range purchases {
		seen[p.InsiderName] = true
	}
	uniqueInsiders := len(seen)

	if uniqueInsiders < d.minInsiders {
		return nil
	}

	// Compute total value of purchases
	var totalValue float64
	for _, p := range purchases {
		totalValue += float64(p.Shares) * p.Price
	}

	// Strength: normalised by expected max of 5 insiders
	strength := math.Min(float64(uniqueInsiders)/5.0, 1.0)

	// Build description
	desc := fmt.Sprintf("%d insider(s) bought over last %d days (total ~$%s)",
		uniqueInsiders, d.lookbackDays, formatMoney(totalValue))

	return &Signal{
		Ticker:      ticker,
		Type:        "insider_buying",
		Description: desc,
		Strength:    strength,
		DetectedAt:  time.Now(),
		Metadata: map[string]interface{}{
			"unique_insiders": uniqueInsiders,
			"total_value":     totalValue,
			"purchases":       len(purchases),
		},
	}
}

func formatMoney(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fK", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}
