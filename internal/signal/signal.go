package signal

import (
	"context"
	"time"
)

// Signal represents a detected trading signal for a ticker.
type Signal struct {
	Ticker      string
	Type        string // "volume_anomaly", "earnings_surprise", "insider_buying"
	Description string
	Strength    float64 // 0.0 to 1.0 (normalised)
	DetectedAt  time.Time
	Metadata    map[string]interface{}
}

// Detector is implemented by each signal type.
type Detector interface {
	Name() string
	Detect(ctx context.Context, tickers []string) ([]Signal, error)
}

// Deduplicate merges signals for the same ticker, keeping the strongest.
// When a ticker has multiple signals, the Description is combined.
func Deduplicate(signals []Signal) []Signal {
	type entry struct {
		signal  Signal
		types   []string
		descs   []string
		maxStr  float64
	}

	byTicker := make(map[string]*entry)
	for _, s := range signals {
		e, ok := byTicker[s.Ticker]
		if !ok {
			cp := s
			byTicker[s.Ticker] = &entry{
				signal: cp,
				types:  []string{s.Type},
				descs:  []string{s.Description},
				maxStr: s.Strength,
			}
			continue
		}
		e.types = append(e.types, s.Type)
		e.descs = append(e.descs, s.Description)
		if s.Strength > e.maxStr {
			e.maxStr = s.Strength
		}
		// Merge metadata
		for k, v := range s.Metadata {
			if e.signal.Metadata == nil {
				e.signal.Metadata = make(map[string]interface{})
			}
			e.signal.Metadata[k] = v
		}
	}

	result := make([]Signal, 0, len(byTicker))
	for _, e := range byTicker {
		s := e.signal
		s.Strength = e.maxStr
		// Build combined type/description
		if len(e.types) > 1 {
			combined := ""
			for i, t := range e.types {
				if i > 0 {
					combined += " + "
				}
				combined += humanType(t)
			}
			s.Type = combined
			desc := ""
			for i, d := range e.descs {
				if i > 0 {
					desc += "; "
				}
				desc += d
			}
			s.Description = desc
		}
		result = append(result, s)
	}
	return result
}

func humanType(t string) string {
	switch t {
	case "volume_anomaly":
		return "Volume Anomaly"
	case "earnings_surprise":
		return "Earnings Surprise"
	case "insider_buying":
		return "Insider Buying"
	default:
		return t
	}
}
