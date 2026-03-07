package source

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const finnhubBase = "https://finnhub.io/api/v1"

// Finnhub is a client for the Finnhub REST API.
// It implements QuoteProvider, NewsProvider, and FundamentalsProvider.
type Finnhub struct {
	apiKey  string
	client  *http.Client
	limiter *RateLimiter
}

// NewFinnhub creates a new Finnhub client with a rate limiter of 1 call/second
// (well within the 60 calls/min free tier).
func NewFinnhub(apiKey string) *Finnhub {
	return &Finnhub{
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 15 * time.Second},
		limiter: NewRateLimiter(1),
	}
}

// FetchCandles retrieves daily OHLCV candles for the past `days` trading days.
func (f *Finnhub) FetchCandles(ticker string, days int) ([]Quote, error) {
	f.limiter.Wait()

	to := time.Now().Unix()
	from := time.Now().AddDate(0, 0, -days*2).Unix() // 2x buffer for weekends/holidays

	url := fmt.Sprintf("%s/stock/candle?symbol=%s&resolution=D&from=%d&to=%d&token=%s",
		finnhubBase, ticker, from, to, f.apiKey)

	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("finnhub: fetch candles %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub: candles %s: status %d", ticker, resp.StatusCode)
	}

	var raw struct {
		C      []float64 `json:"c"` // close
		H      []float64 `json:"h"` // high
		L      []float64 `json:"l"` // low
		O      []float64 `json:"o"` // open
		V      []float64 `json:"v"` // volume
		T      []int64   `json:"t"` // unix timestamp
		Status string    `json:"s"` // "ok" or "no_data"
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("finnhub: decode candles %s: %w", ticker, err)
	}

	if raw.Status != "ok" || len(raw.T) == 0 {
		return nil, nil
	}

	// Return last `days` candles
	start := 0
	if len(raw.T) > days {
		start = len(raw.T) - days
	}

	quotes := make([]Quote, 0, len(raw.T)-start)
	for i := start; i < len(raw.T); i++ {
		quotes = append(quotes, Quote{
			Ticker: ticker,
			Open:   raw.O[i],
			High:   raw.H[i],
			Low:    raw.L[i],
			Close:  raw.C[i],
			Volume: int64(raw.V[i]),
			Date:   time.Unix(raw.T[i], 0).UTC(),
		})
	}
	return quotes, nil
}

// FetchNews retrieves recent news articles for a ticker.
func (f *Finnhub) FetchNews(ticker string, days int) ([]NewsItem, error) {
	f.limiter.Wait()

	to := time.Now().Format("2006-01-02")
	from := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	url := fmt.Sprintf("%s/company-news?symbol=%s&from=%s&to=%s&token=%s",
		finnhubBase, ticker, from, to, f.apiKey)

	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("finnhub: fetch news %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub: news %s: status %d", ticker, resp.StatusCode)
	}

	var raw []struct {
		Category  string  `json:"category"`
		DateTime  int64   `json:"datetime"`
		Headline  string  `json:"headline"`
		ID        int64   `json:"id"`
		Image     string  `json:"image"`
		Related   string  `json:"related"`
		Source    string  `json:"source"`
		Summary   string  `json:"summary"`
		URL       string  `json:"url"`
		Sentiment float64 `json:"sentiment"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("finnhub: decode news %s: %w", ticker, err)
	}

	items := make([]NewsItem, 0, len(raw))
	for _, r := range raw {
		items = append(items, NewsItem{
			Ticker:    ticker,
			Headline:  r.Headline,
			Summary:   r.Summary,
			Source:    r.Source,
			URL:       r.URL,
			Sentiment: r.Sentiment,
			Published: time.Unix(r.DateTime, 0).UTC(),
		})
	}
	return items, nil
}

// FetchFundamentals retrieves key financial metrics for a ticker.
func (f *Finnhub) FetchFundamentals(ticker string) (*Fundamentals, error) {
	f.limiter.Wait()

	url := fmt.Sprintf("%s/stock/metric?symbol=%s&metric=all&token=%s",
		finnhubBase, ticker, f.apiKey)

	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("finnhub: fetch fundamentals %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnhub: fundamentals %s: status %d", ticker, resp.StatusCode)
	}

	var raw struct {
		Metric struct {
			PE               float64 `json:"peNormalizedAnnual"`
			ForwardPE        float64 `json:"peTTM"`
			High52W          float64 `json:"52WeekHigh"`
			Low52W           float64 `json:"52WeekLow"`
			MarketCap        float64 `json:"marketCapitalization"`
			EPS              float64 `json:"epsNormalizedAnnual"`
			GrossMargin      float64 `json:"grossMarginAnnual"`
			OpMargin         float64 `json:"operatingMarginAnnual"`
			RevenueGrowth    float64 `json:"revenueGrowthQuarterlyYoy"`
			DE               float64 `json:"totalDebt/totalEquityAnnual"`
			FCF              float64 `json:"freeCashFlowAnnual"`
		} `json:"metric"`
		Series struct {
			Annual struct {
				Revenue []struct {
					Period string  `json:"period"`
					V      float64 `json:"v"`
				} `json:"revenue"`
			} `json:"annual"`
		} `json:"series"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("finnhub: decode fundamentals %s: %w", ticker, err)
	}

	// Fetch current quote for price
	f.limiter.Wait()
	quoteURL := fmt.Sprintf("%s/quote?symbol=%s&token=%s", finnhubBase, ticker, f.apiKey)
	qresp, err := f.client.Get(quoteURL)
	var currentPrice float64
	if err == nil && qresp.StatusCode == http.StatusOK {
		var q struct {
			C float64 `json:"c"` // current price
		}
		_ = json.NewDecoder(qresp.Body).Decode(&q)
		qresp.Body.Close()
		currentPrice = q.C
	}

	var revenue float64
	if len(raw.Series.Annual.Revenue) > 0 {
		revenue = raw.Series.Annual.Revenue[0].V * 1e6 // convert millions
	}

	return &Fundamentals{
		Ticker:        ticker,
		CurrentPrice:  currentPrice,
		MarketCap:     raw.Metric.MarketCap * 1e6, // convert millions
		PE:            raw.Metric.PE,
		ForwardPE:     raw.Metric.ForwardPE,
		Revenue:       revenue,
		RevenueGrowth: raw.Metric.RevenueGrowth,
		EPS:           raw.Metric.EPS,
		GrossMargin:   raw.Metric.GrossMargin,
		OpMargin:      raw.Metric.OpMargin,
		DE:            raw.Metric.DE,
		FCF:           raw.Metric.FCF * 1e6,
		High52W:       raw.Metric.High52W,
		Low52W:        raw.Metric.Low52W,
	}, nil
}
