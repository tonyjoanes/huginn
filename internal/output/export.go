package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tonyjoanes/huginn/internal/analyst"
)

// ExportJSON writes analyses to a JSON file.
func ExportJSON(analyses []*analyst.Analysis, path string) error {
	data, err := json.MarshalIndent(analyses, "", "  ")
	if err != nil {
		return fmt.Errorf("export json: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("export json: write %s: %w", path, err)
	}
	fmt.Printf("Exported %d analyses to %s\n", len(analyses), path)
	return nil
}

// ExportMarkdown writes analyses to a Markdown report file.
func ExportMarkdown(analyses []*analyst.Analysis, path string) error {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Huginn Stock Research Report\n"))
	sb.WriteString(fmt.Sprintf("Generated: %s\n\n", time.Now().Format("2006-01-02 15:04 MST")))
	sb.WriteString("---\n\n")

	for _, a := range analyses {
		sb.WriteString(fmt.Sprintf("## %s — %s\n\n", a.Ticker, a.SignalType))
		sb.WriteString(fmt.Sprintf("**Signal:** %s\n\n", a.SignalDesc))
		sb.WriteString(fmt.Sprintf("**Conviction:** %d/10 | **Verdict:** %s\n\n", a.ConvictionScore, a.Verdict))

		if a.Thesis != "" {
			sb.WriteString("### Thesis\n")
			sb.WriteString(a.Thesis + "\n\n")
		}

		if len(a.BullCase) > 0 {
			sb.WriteString("### Bull Case\n")
			for _, b := range a.BullCase {
				sb.WriteString(fmt.Sprintf("- %s\n", b))
			}
			sb.WriteString("\n")
		}

		if len(a.BearCase) > 0 {
			sb.WriteString("### Bear Case\n")
			for _, b := range a.BearCase {
				sb.WriteString(fmt.Sprintf("- %s\n", b))
			}
			sb.WriteString("\n")
		}

		sb.WriteString("### Trade Plan\n")
		sb.WriteString(fmt.Sprintf("| Entry | Stop Loss | Target 1 | Target 2 | Hold |\n"))
		sb.WriteString(fmt.Sprintf("|-------|-----------|----------|----------|------|\n"))
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n\n",
			fmtPrice(a.TradePlan.Entry),
			fmtPrice(a.TradePlan.StopLoss),
			fmtPrice(a.TradePlan.Target1),
			fmtPrice(a.TradePlan.Target2),
			a.TradePlan.TimeHorizon,
		))

		sb.WriteString("---\n\n")
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("export markdown: write %s: %w", path, err)
	}
	fmt.Printf("Exported %d analyses to %s\n", len(analyses), path)
	return nil
}
