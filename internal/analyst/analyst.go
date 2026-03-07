package analyst

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/tonyjoanes/huginn/internal/enricher"
)

// TradePlan holds the structured trade recommendation from Claude.
type TradePlan struct {
	Entry       float64
	StopLoss    float64
	Target1     float64
	Target2     float64
	TimeHorizon string
	RawText     string
}

// Analysis is the parsed output from Claude's analysis.
type Analysis struct {
	Ticker          string
	SignalType       string
	SignalDesc       string
	Thesis          string
	BullCase        []string
	BearCase        []string
	ConvictionScore int
	TradePlan       TradePlan
	Verdict         string // BUY / WATCHLIST / SKIP
	RawResponse     string
}

// Analyst calls Claude's API to produce a structured analysis.
type Analyst struct {
	client     anthropic.Client
	model      string
	maxTokens  int
	tmplPath   string
}

// New creates an Analyst using the given API key, model, and prompt template path.
func New(apiKey, model string, maxTokens int, tmplPath string) *Analyst {
	return &Analyst{
		client:    anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:     model,
		maxTokens: maxTokens,
		tmplPath:  tmplPath,
	}
}

// Analyse sends enriched stock data to Claude and parses the structured response.
func (a *Analyst) Analyse(ctx context.Context, data *enricher.EnrichedData) (*Analysis, error) {
	prompt, err := BuildPrompt(a.tmplPath, data)
	if err != nil {
		return nil, fmt.Errorf("analyst: build prompt for %s: %w", data.Signal.Ticker, err)
	}

	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: int64(a.maxTokens),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("analyst: claude API for %s: %w", data.Signal.Ticker, err)
	}

	var rawText string
	for _, block := range msg.Content {
		if block.Type == "text" {
			rawText = block.Text
			break
		}
	}

	analysis := parseResponse(rawText)
	analysis.Ticker = data.Signal.Ticker
	analysis.SignalType = data.Signal.Type
	analysis.SignalDesc = data.Signal.Description
	analysis.RawResponse = rawText

	return analysis, nil
}

// BuildPromptOnly builds the prompt without calling Claude (for test-prompt command).
func (a *Analyst) BuildPromptOnly(data *enricher.EnrichedData) (string, error) {
	return BuildPrompt(a.tmplPath, data)
}

// parseResponse extracts structured fields from Claude's text response.
func parseResponse(text string) *Analysis {
	a := &Analysis{}

	// Extract sections by looking for numbered headers
	a.Thesis = extractSection(text, "1. THESIS", "2.")
	a.BullCase = extractBulletPoints(extractSection(text, "2. BULL CASE", "3."))
	a.BearCase = extractBulletPoints(extractSection(text, "3. BEAR CASE", "4."))

	// Conviction score: look for "7/10" or "CONVICTION SCORE: 7"
	a.ConvictionScore = extractConviction(text)

	// Trade plan
	tradePlanText := extractSection(text, "5. TRADE PLAN", "6.")
	a.TradePlan = parseTradePlan(tradePlanText)

	// Verdict
	a.Verdict = extractVerdict(text)

	return a
}

func extractSection(text, start, end string) string {
	// Find the start marker (case-insensitive)
	lower := strings.ToLower(text)
	lowerStart := strings.ToLower(start)

	si := strings.Index(lower, lowerStart)
	if si == -1 {
		// Try without number prefix
		parts := strings.SplitN(start, " ", 2)
		if len(parts) > 1 {
			lowerStart = strings.ToLower(parts[1])
			si = strings.Index(lower, lowerStart)
		}
	}
	if si == -1 {
		return ""
	}
	si += len(lowerStart)

	// Find end marker
	lowerEnd := strings.ToLower(end)
	ei := strings.Index(lower[si:], lowerEnd)
	if ei == -1 {
		// Take to end of text
		return strings.TrimSpace(text[si:])
	}
	return strings.TrimSpace(text[si : si+ei])
}

func extractBulletPoints(text string) []string {
	lines := strings.Split(text, "\n")
	var points []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip common bullet markers
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "•")
		line = strings.TrimPrefix(line, "*")
		// Strip numbered list items like "1." "2." "3."
		if len(line) > 2 && line[0] >= '1' && line[0] <= '9' && line[1] == '.' {
			line = line[2:]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			points = append(points, line)
		}
	}
	return points
}

var convictionRe = regexp.MustCompile(`(\d+)\s*/\s*10`)

func extractConviction(text string) int {
	matches := convictionRe.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0
	}
	n, err := strconv.Atoi(matches[1])
	if err != nil || n < 1 || n > 10 {
		return 0
	}
	return n
}

var priceRe = regexp.MustCompile(`\$\s*([\d,.]+)`)

func extractPrice(text string) float64 {
	m := priceRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	s := strings.ReplaceAll(m[1], ",", "")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseTradePlan(text string) TradePlan {
	tp := TradePlan{RawText: text}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "entry"):
			tp.Entry = extractPrice(line)
		case strings.Contains(lower, "stop"):
			tp.StopLoss = extractPrice(line)
		case strings.Contains(lower, "target 1") || strings.Contains(lower, "target1"):
			tp.Target1 = extractPrice(line)
		case strings.Contains(lower, "target 2") || strings.Contains(lower, "target2"):
			tp.Target2 = extractPrice(line)
		case strings.Contains(lower, "horizon") || strings.Contains(lower, "hold"):
			// Extract text after the colon
			if idx := strings.Index(line, ":"); idx != -1 {
				tp.TimeHorizon = strings.TrimSpace(line[idx+1:])
			}
		}
	}
	return tp
}

func extractVerdict(text string) string {
	lower := strings.ToLower(text)

	// Look for "VERDICT: BUY" etc
	verdictIdx := strings.Index(lower, "verdict")
	if verdictIdx != -1 {
		snippet := text[verdictIdx:]
		if len(snippet) > 30 {
			snippet = snippet[:30]
		}
		upper := strings.ToUpper(snippet)
		switch {
		case strings.Contains(upper, "BUY"):
			return "BUY"
		case strings.Contains(upper, "WATCHLIST"):
			return "WATCHLIST"
		case strings.Contains(upper, "SKIP"):
			return "SKIP"
		}
	}

	// Fallback: scan full text
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "VERDICT: BUY"):
		return "BUY"
	case strings.Contains(upper, "VERDICT: WATCHLIST"):
		return "WATCHLIST"
	case strings.Contains(upper, "VERDICT: SKIP"):
		return "SKIP"
	}

	return "UNKNOWN"
}
