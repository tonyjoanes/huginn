package analyst

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"

	"github.com/tonyjoanes/huginn/internal/enricher"
	"github.com/tonyjoanes/huginn/internal/source"
)

// PromptData is passed to the analysis template.
type PromptData struct {
	Signal       interface{} // signal.Signal
	Fundamentals *source.Fundamentals
	Candles      []source.Quote
	News         []source.NewsItem
	Insiders     []source.InsiderTrade
	AvgVolume    int64
	TodayVolume  int64
}

// BuildPrompt renders the analysis prompt template with enriched data.
func BuildPrompt(tmplPath string, data *enricher.EnrichedData) (string, error) {
	funcMap := template.FuncMap{
		"formatDate":     formatDate,
		"formatCurrency": formatCurrency,
		"formatVolume":   formatVolume,
		"formatVolume64": formatVolume64,
		"formatOHLCV":    formatOHLCV,
		"printf":         fmt.Sprintf,
	}

	tmpl, err := template.New("analysis.tmpl").Funcs(funcMap).ParseFiles(tmplPath)
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", tmplPath, err)
	}

	pd := PromptData{
		Signal:       data.Signal,
		Fundamentals: data.Fundamentals,
		Candles:      data.Candles,
		News:         data.News,
		Insiders:     data.Insiders,
		AvgVolume:    data.AvgVolume(),
		TodayVolume:  data.TodayVolume(),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, pd); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

func formatDate(t time.Time) string {
	return t.Format("2006-01-02")
}

func formatCurrency(v float64) string {
	switch {
	case math.Abs(v) >= 1e12:
		return fmt.Sprintf("$%.2fT", v/1e12)
	case math.Abs(v) >= 1e9:
		return fmt.Sprintf("$%.2fB", v/1e9)
	case math.Abs(v) >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case math.Abs(v) >= 1e3:
		return fmt.Sprintf("$%.1fK", v/1e3)
	default:
		return fmt.Sprintf("$%.2f", v)
	}
}

func formatVolume(v int64) string {
	return formatVolume64(v)
}

func formatVolume64(v int64) string {
	fv := float64(v)
	switch {
	case fv >= 1e9:
		return fmt.Sprintf("%.1fB", fv/1e9)
	case fv >= 1e6:
		return fmt.Sprintf("%.1fM", fv/1e6)
	case fv >= 1e3:
		return fmt.Sprintf("%.1fK", fv/1e3)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// formatOHLCV renders the last 20 candles as a compact text table.
func formatOHLCV(candles []source.Quote) string {
	if len(candles) == 0 {
		return "No price data available."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-12s %8s %8s %8s %8s %12s\n",
		"Date", "Open", "High", "Low", "Close", "Volume"))
	sb.WriteString(strings.Repeat("-", 62) + "\n")

	// Show last 20 days
	start := 0
	if len(candles) > 20 {
		start = len(candles) - 20
	}
	for _, c := range candles[start:] {
		sb.WriteString(fmt.Sprintf("%-12s %8.2f %8.2f %8.2f %8.2f %12s\n",
			c.Date.Format("2006-01-02"),
			c.Open, c.High, c.Low, c.Close,
			formatVolume64(c.Volume),
		))
	}
	return sb.String()
}
