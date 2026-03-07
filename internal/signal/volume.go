package signal

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/tonyjoanes/huginn/internal/source"
)

// VolumeDetector detects volume anomalies: today's volume > multiplier × 20-day avg
// AND abs price move > minPriceChange.
type VolumeDetector struct {
	provider         source.QuoteProvider
	volumeMultiplier float64
	minPriceChange   float64
}

func NewVolumeDetector(p source.QuoteProvider, multiplier, minPriceChange float64) *VolumeDetector {
	return &VolumeDetector{
		provider:         p,
		volumeMultiplier: multiplier,
		minPriceChange:   minPriceChange,
	}
}

func (d *VolumeDetector) Name() string { return "volume_anomaly" }

func (d *VolumeDetector) Detect(ctx context.Context, tickers []string) ([]Signal, error) {
	const maxConcurrent = 10
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

func (d *VolumeDetector) checkTicker(ticker string) *Signal {
	candles, err := d.provider.FetchCandles(ticker, 21)
	if err != nil || len(candles) < 2 {
		return nil
	}

	// Most recent candle is "today"
	today := candles[len(candles)-1]
	historical := candles[:len(candles)-1]

	// Compute 20-day average volume
	var totalVol int64
	for _, c := range historical {
		totalVol += c.Volume
	}
	avgVol := float64(totalVol) / float64(len(historical))
	if avgVol == 0 {
		return nil
	}

	// Volume ratio
	volRatio := float64(today.Volume) / avgVol
	if volRatio < d.volumeMultiplier {
		return nil
	}

	// Price move check
	if today.Open == 0 {
		return nil
	}
	priceMove := math.Abs(today.Close-today.Open) / today.Open
	if priceMove < d.minPriceChange {
		return nil
	}

	// Normalised strength: capped at 1.0
	strength := math.Min(volRatio/d.volumeMultiplier, 1.0)

	direction := "up"
	if today.Close < today.Open {
		direction = "down"
	}

	return &Signal{
		Ticker: ticker,
		Type:   "volume_anomaly",
		Description: fmt.Sprintf("Volume %.1fx avg (%.0f vs %.0f); price moved %.1f%% %s",
			volRatio, float64(today.Volume), avgVol, priceMove*100, direction),
		Strength:   strength,
		DetectedAt: time.Now(),
		Metadata: map[string]interface{}{
			"volume_ratio":  volRatio,
			"avg_volume":    int64(avgVol),
			"today_volume":  today.Volume,
			"price_change":  priceMove,
			"today_close":   today.Close,
		},
	}
}
