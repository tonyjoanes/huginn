package output

import (
	"fmt"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/tonyjoanes/huginn/internal/analyst"
)

// ANSI colour codes
const (
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colRed    = "\033[31m"
	colReset  = "\033[0m"
)

// RenderTable prints a formatted terminal table of analyses.
func RenderTable(analyses []*analyst.Analysis, minConviction int) {
	fmt.Printf("\n%s═══ Huginn Stock Screener Results ═══%s\n\n", colGreen, colReset)

	table := tablewriter.NewWriter(os.Stdout)
	table.Header([]string{"Ticker", "Signal", "Conviction", "Entry", "Stop", "Target 1", "Verdict"})

	shown := 0
	for _, a := range analyses {
		if a.ConvictionScore < minConviction {
			continue
		}

		conviction := fmt.Sprintf("%d/10", a.ConvictionScore)
		entry := fmtPrice(a.TradePlan.Entry)
		stop := fmtPrice(a.TradePlan.StopLoss)
		target1 := fmtPrice(a.TradePlan.Target1)

		sigType := a.SignalType
		if len(sigType) > 25 {
			sigType = sigType[:22] + "..."
		}

		_ = table.Append([]string{
			a.Ticker,
			sigType,
			conviction,
			entry,
			stop,
			target1,
			a.Verdict,
		})
		shown++
	}

	if shown == 0 {
		fmt.Println("No results meet the minimum conviction threshold.")
		return
	}

	_ = table.Render()
	fmt.Printf("\n%d stock(s) shown (min conviction: %d/10)\n", shown, minConviction)
	fmt.Printf("Run 'huginn detail <TICKER>' for the full thesis.\n\n")
}

// PrintDetail prints the full analysis for a single ticker.
func PrintDetail(a *analyst.Analysis) {
	fmt.Printf("\n%s╔══ %s — %s ══╗%s\n\n", colGreen, a.Ticker, a.SignalType, colReset)
	fmt.Printf("%sSignal:%s %s\n\n", colYellow, colReset, a.SignalDesc)
	fmt.Printf("%sTHESIS%s\n%s\n\n", colYellow, colReset, a.Thesis)

	fmt.Printf("%sBULL CASE%s\n", colGreen, colReset)
	for i, b := range a.BullCase {
		fmt.Printf("  %d. %s\n", i+1, b)
	}
	fmt.Println()

	fmt.Printf("%sBEAR CASE%s\n", colRed, colReset)
	for i, b := range a.BearCase {
		fmt.Printf("  %d. %s\n", i+1, b)
	}
	fmt.Println()

	fmt.Printf("%sCONVICTION:%s %d/10\n\n", colYellow, colReset, a.ConvictionScore)

	fmt.Printf("%sTRADE PLAN%s\n", colYellow, colReset)
	if a.TradePlan.RawText != "" {
		for _, line := range strings.Split(a.TradePlan.RawText, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	} else {
		fmt.Printf("  Entry: %s | Stop: %s | T1: %s | T2: %s | Hold: %s\n",
			fmtPrice(a.TradePlan.Entry),
			fmtPrice(a.TradePlan.StopLoss),
			fmtPrice(a.TradePlan.Target1),
			fmtPrice(a.TradePlan.Target2),
			a.TradePlan.TimeHorizon,
		)
	}
	fmt.Println()
	fmt.Printf("%sVERDICT:%s %s\n\n", colYellow, colReset, a.Verdict)
}

func fmtPrice(p float64) string {
	if p == 0 {
		return "—"
	}
	return fmt.Sprintf("$%.2f", p)
}
