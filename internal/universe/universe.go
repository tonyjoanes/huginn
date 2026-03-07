package universe

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// TickerEntry represents one stock in the universe.
type TickerEntry struct {
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
	Sector string `json:"sector"`
}

// Universe holds the set of stocks to screen.
type Universe struct {
	Tickers []TickerEntry
}

// LoadFromFile reads the universe from a JSON file.
func LoadFromFile(path string) (*Universe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("universe: read %s: %w", path, err)
	}

	var entries []TickerEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("universe: decode %s: %w", path, err)
	}

	return &Universe{Tickers: entries}, nil
}

// TickerSymbols returns just the ticker strings.
func (u *Universe) TickerSymbols() []string {
	symbols := make([]string, len(u.Tickers))
	for i, t := range u.Tickers {
		symbols[i] = t.Ticker
	}
	return symbols
}

// Find returns the TickerEntry for a given symbol, or nil if not found.
func (u *Universe) Find(ticker string) *TickerEntry {
	upper := strings.ToUpper(ticker)
	for i := range u.Tickers {
		if strings.ToUpper(u.Tickers[i].Ticker) == upper {
			return &u.Tickers[i]
		}
	}
	return nil
}

// Add appends a new ticker (or updates existing) in the universe.
func (u *Universe) Add(entry TickerEntry) {
	entry.Ticker = strings.ToUpper(entry.Ticker)
	for i, t := range u.Tickers {
		if strings.ToUpper(t.Ticker) == entry.Ticker {
			u.Tickers[i] = entry
			return
		}
	}
	u.Tickers = append(u.Tickers, entry)
}

// SaveToFile writes the universe back to a JSON file.
func (u *Universe) SaveToFile(path string) error {
	data, err := json.MarshalIndent(u.Tickers, "", "  ")
	if err != nil {
		return fmt.Errorf("universe: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("universe: write %s: %w", path, err)
	}
	return nil
}
