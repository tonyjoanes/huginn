package source

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	edgarBase     = "https://data.sec.gov"
	edgarTickerURL = "https://www.sec.gov/files/company_tickers.json"
)

// EDGAR is a client for SEC EDGAR's REST API.
// No API key required — only a descriptive User-Agent per EDGAR fair use policy.
type EDGAR struct {
	userAgent string
	client    *http.Client
	limiter   *RateLimiter

	// CIK lookup cache: ticker -> CIK (zero-padded to 10 digits)
	cikMu    sync.RWMutex
	cikCache map[string]string
}

// NewEDAR creates a new EDGAR client. The userAgent should include contact info
// per SEC EDGAR fair use policy (e.g. "Huginn/1.0 user@example.com").
func NewEDAGAR(userAgent string) *EDGAR {
	return &EDGAR{
		userAgent: userAgent,
		client:    &http.Client{Timeout: 15 * time.Second},
		limiter:   NewRateLimiter(8), // 8 req/sec (EDGAR recommends ≤10)
		cikCache:  make(map[string]string),
	}
}

// FetchInsiderTrades returns Form 4 purchases (code "P") within the last `days` days.
func (e *EDGAR) FetchInsiderTrades(ticker string, days int) ([]InsiderTrade, error) {
	cik, err := e.lookupCIK(ticker)
	if err != nil {
		return nil, fmt.Errorf("edgar: CIK lookup %s: %w", ticker, err)
	}
	if cik == "" {
		return nil, nil
	}

	e.limiter.Wait()

	url := fmt.Sprintf("%s/submissions/CIK%s.json", edgarBase, cik)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", e.userAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("edgar: fetch submissions %s: %w", ticker, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("edgar: submissions %s: status %d", ticker, resp.StatusCode)
	}

	var raw struct {
		Name    string `json:"name"`
		Filings struct {
			Recent struct {
				AccessionNumber    []string  `json:"accessionNumber"`
				FilingDate         []string  `json:"filingDate"`
				Form               []string  `json:"form"`
				PrimaryDocument    []string  `json:"primaryDocument"`
			} `json:"recent"`
		} `json:"filings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("edgar: decode submissions %s: %w", ticker, err)
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	var trades []InsiderTrade

	for i, form := range raw.Filings.Recent.Form {
		if form != "4" {
			continue
		}
		if i >= len(raw.Filings.Recent.FilingDate) {
			break
		}
		filedAt, err := time.Parse("2006-01-02", raw.Filings.Recent.FilingDate[i])
		if err != nil || filedAt.Before(cutoff) {
			continue
		}

		// Fetch the individual Form 4 XML to get transaction details
		accession := raw.Filings.Recent.AccessionNumber[i]
		doc := raw.Filings.Recent.PrimaryDocument[i]
		formTrades, err := e.parseForm4(ticker, cik, accession, doc, filedAt)
		if err != nil {
			continue // tolerate parse errors
		}
		trades = append(trades, formTrades...)
	}

	return trades, nil
}

func (e *EDGAR) parseForm4(ticker, cik, accession, doc string, filedAt time.Time) ([]InsiderTrade, error) {
	e.limiter.Wait()

	// Convert accession number to path format (remove dashes)
	accPath := strings.ReplaceAll(accession, "-", "")
	url := fmt.Sprintf("%s/Archives/edgar/data/%s/%s/%s",
		edgarBase,
		strings.TrimLeft(cik, "0"),
		accPath,
		doc,
	)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", e.userAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("edgar: form4 status %d", resp.StatusCode)
	}

	// Read the body as text (XML) and do simple text extraction
	// A proper implementation would use an XML parser; this extracts key fields
	buf := new(strings.Builder)
	var b [65536]byte
	for {
		n, err := resp.Body.Read(b[:])
		if n > 0 {
			buf.Write(b[:n])
		}
		if err != nil {
			break
		}
	}

	content := buf.String()
	return parseForm4XML(ticker, content, filedAt), nil
}

// parseForm4XML extracts insider trade data from Form 4 XML content.
func parseForm4XML(ticker, content string, filedAt time.Time) []InsiderTrade {
	var trades []InsiderTrade

	// Extract reporter name
	insiderName := extractXMLField(content, "rptOwnerName")
	title := extractXMLField(content, "officerTitle")
	if title == "" {
		title = "Director/Officer"
	}

	// Look for non-derivative transaction entries
	// Transaction code "P" = purchase, shares, price per share
	txCode := extractXMLField(content, "transactionCode")
	if txCode != "P" {
		return trades
	}

	// Check for 10b5-1 automatic plan indicator
	isAutomatic := strings.Contains(content, "<transactionTimeliness>L</transactionTimeliness>") ||
		strings.Contains(content, "10b5-1")

	sharesStr := extractXMLField(content, "transactionShares")
	priceStr := extractXMLField(content, "transactionPricePerShare")

	var shares float64
	var price float64
	fmt.Sscanf(sharesStr, "%f", &shares)
	fmt.Sscanf(priceStr, "%f", &price)

	if shares > 0 {
		trades = append(trades, InsiderTrade{
			Ticker:          ticker,
			InsiderName:     insiderName,
			Title:           title,
			TransactionType: txCode,
			Shares:          int64(shares),
			Price:           price,
			Date:            filedAt,
			IsAutomatic:     isAutomatic,
		})
	}

	return trades
}

// extractXMLField extracts the text content of the first matching XML element.
func extractXMLField(content, field string) string {
	open := "<" + field + ">"
	close_ := "</" + field + ">"
	start := strings.Index(content, open)
	if start == -1 {
		return ""
	}
	start += len(open)
	end := strings.Index(content[start:], close_)
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}

// lookupCIK finds the SEC CIK number for a ticker symbol.
func (e *EDGAR) lookupCIK(ticker string) (string, error) {
	e.cikMu.RLock()
	if cik, ok := e.cikCache[ticker]; ok {
		e.cikMu.RUnlock()
		return cik, nil
	}
	e.cikMu.RUnlock()

	// Fetch the bulk ticker→CIK map from EDGAR on first call
	e.limiter.Wait()
	req, _ := http.NewRequest("GET", edgarTickerURL, nil)
	req.Header.Set("User-Agent", e.userAgent)

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("edgar: fetch ticker map: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("edgar: ticker map status %d", resp.StatusCode)
	}

	// The JSON is: {"0": {"cik_str": 320193, "ticker": "AAPL", "title": "Apple Inc."}, ...}
	var raw map[string]struct {
		CIK    int    `json:"cik_str"`
		Ticker string `json:"ticker"`
		Title  string `json:"title"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("edgar: decode ticker map: %w", err)
	}

	e.cikMu.Lock()
	for _, v := range raw {
		padded := fmt.Sprintf("%010d", v.CIK)
		e.cikCache[strings.ToUpper(v.Ticker)] = padded
	}
	e.cikMu.Unlock()

	e.cikMu.RLock()
	cik := e.cikCache[strings.ToUpper(ticker)]
	e.cikMu.RUnlock()
	return cik, nil
}
