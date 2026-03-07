package source

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const fmpBase = "https://financialmodelingprep.com"

// FMP is a client for the Financial Modeling Prep REST API.
// It implements EarningsProvider and FundamentalsProvider.
type FMP struct {
	apiKey  string
	client  *http.Client
	limiter *RateLimiter
}

// NewFMP creates a new FMP client. The free tier allows 250 calls/day.
// We use 1 call per 350ms to stay comfortably within limits.
func NewFMP(apiKey string) *FMP {
	return &FMP{
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 15 * time.Second},
		limiter: NewRateLimiter(1),
	}
}

// FetchEarnings retrieves the most recent earnings surprise for a ticker.
func (f *FMP) FetchEarnings(ticker string) (*EarningsData, error) {
	f.limiter.Wait()

	url := fmt.Sprintf("%s/api/v3/earnings-surprises/%s?apikey=%s",
		fmpBase, ticker, f.apiKey)

	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fmp: fetch earnings %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fmp: earnings %s: status %d", ticker, resp.StatusCode)
	}

	var raw []struct {
		Symbol            string  `json:"symbol"`
		Date              string  `json:"date"`
		ActualEarningResult float64 `json:"actualEarningResult"`
		EstimatedEarning  float64 `json:"estimatedEarning"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("fmp: decode earnings %s: %w", ticker, err)
	}

	if len(raw) == 0 {
		return nil, nil
	}

	r := raw[0] // most recent
	var reportedAt time.Time
	if t, err := time.Parse("2006-01-02", r.Date); err == nil {
		reportedAt = t
	}

	var surprise float64
	if r.EstimatedEarning != 0 {
		surprise = (r.ActualEarningResult - r.EstimatedEarning) / abs(r.EstimatedEarning)
	}

	return &EarningsData{
		Ticker:       ticker,
		ActualEPS:    r.ActualEarningResult,
		EstimatedEPS: r.EstimatedEarning,
		Surprise:     surprise,
		ReportedAt:   reportedAt,
	}, nil
}

// FetchFundamentals retrieves key financial ratios and income statement data.
func (f *FMP) FetchFundamentals(ticker string) (*Fundamentals, error) {
	f.limiter.Wait()

	url := fmt.Sprintf("%s/api/v3/ratios/%s?limit=1&apikey=%s",
		fmpBase, ticker, f.apiKey)

	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fmp: fetch ratios %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fmp: ratios %s: status %d", ticker, resp.StatusCode)
	}

	var rawRatios []struct {
		Symbol          string  `json:"symbol"`
		PERatio         float64 `json:"priceEarningsRatio"`
		GrossMargin     float64 `json:"grossProfitMargin"`
		OpMargin        float64 `json:"operatingProfitMargin"`
		DebtEquity      float64 `json:"debtEquityRatio"`
		FreeCashFlow    float64 `json:"freeCashFlowPerShare"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rawRatios); err != nil {
		return nil, fmt.Errorf("fmp: decode ratios %s: %w", ticker, err)
	}

	var fund Fundamentals
	fund.Ticker = ticker

	if len(rawRatios) > 0 {
		r := rawRatios[0]
		fund.PE = r.PERatio
		fund.GrossMargin = r.GrossMargin * 100
		fund.OpMargin = r.OpMargin * 100
		fund.DE = r.DebtEquity
	}

	// Fetch income statement for revenue
	f.limiter.Wait()
	incomeURL := fmt.Sprintf("%s/api/v3/income-statement/%s?limit=4&apikey=%s",
		fmpBase, ticker, f.apiKey)

	iresp, err := f.client.Get(incomeURL)
	if err == nil && iresp.StatusCode == http.StatusOK {
		var rawIncome []struct {
			Revenue             float64 `json:"revenue"`
			RevenueGrowth       float64 `json:"revenueGrowth"`
			EPS                 float64 `json:"eps"`
			NetIncome           float64 `json:"netIncome"`
		}
		if err := json.NewDecoder(iresp.Body).Decode(&rawIncome); err == nil && len(rawIncome) > 0 {
			fund.Revenue = rawIncome[0].Revenue
			fund.EPS = rawIncome[0].EPS
			if len(rawIncome) > 1 && rawIncome[1].Revenue != 0 {
				fund.RevenueGrowth = ((rawIncome[0].Revenue - rawIncome[1].Revenue) / rawIncome[1].Revenue) * 100
			}
		}
		iresp.Body.Close()
	}

	return &fund, nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
