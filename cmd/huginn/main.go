package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tonyjoanes/huginn/internal/analyst"
	"github.com/tonyjoanes/huginn/internal/config"
	"github.com/tonyjoanes/huginn/internal/enricher"
	"github.com/tonyjoanes/huginn/internal/output"
	"github.com/tonyjoanes/huginn/internal/signal"
	"github.com/tonyjoanes/huginn/internal/source"
	"github.com/tonyjoanes/huginn/internal/store"
	"github.com/tonyjoanes/huginn/internal/universe"
)

var configPath string

func main() {
	root := &cobra.Command{
		Use:   "huginn",
		Short: "Huginn — AI stock research tool",
		Long: `Huginn (named after Odin's raven) scans the stock market daily for
trading opportunities using signal detection, data enrichment, and Claude AI analysis.`,
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "path to config.yaml")

	root.AddCommand(
		newRunCmd(),
		newDetailCmd(),
		newHistoryCmd(),
		newAddTickerCmd(),
		newTestPromptCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── huginn run ────────────────────────────────────────────────────────────────

func newRunCmd() *cobra.Command {
	var signalFilter string
	var exportFormat string

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the full daily scan",
		Long:  "Detect signals, enrich candidates, and generate AI analyses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(signalFilter, exportFormat)
		},
	}
	cmd.Flags().StringVarP(&signalFilter, "signal", "s", "", "only run one signal type: volume, earnings, insider")
	cmd.Flags().StringVarP(&exportFormat, "export", "e", "", "export results: json or markdown")
	return cmd
}

func runScan(signalFilter, exportFormat string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	uni, err := loadUniverse(cfg)
	if err != nil {
		return err
	}

	db, err := openStore(cfg)
	if err != nil {
		log.Printf("warn: could not open store: %v — history will not be saved", err)
	}
	if db != nil {
		defer db.Close()
	}

	tickers := uni.TickerSymbols()
	fmt.Printf("[1/4] Loaded universe: %d tickers\n", len(tickers))

	// ── Build sources ──────────────────────────────────────────────────────
	finnhub := source.NewFinnhub(cfg.APIKeys.Finnhub)
	fmp := source.NewFMP(cfg.APIKeys.FMP)
	edgar := source.NewEDAGAR("Huginn/1.0 github.com/tonyjoanes/huginn")

	// ── Build signal detectors ─────────────────────────────────────────────
	var detectors []signal.Detector

	if shouldRun(signalFilter, "volume") && cfg.Signals.VolumeAnomaly.Enabled {
		detectors = append(detectors, signal.NewVolumeDetector(
			finnhub,
			cfg.Signals.VolumeAnomaly.VolumeMultiplier,
			cfg.Signals.VolumeAnomaly.MinPriceChange,
		))
	}
	if shouldRun(signalFilter, "earnings") && cfg.Signals.EarningsSurprise.Enabled {
		detectors = append(detectors, signal.NewEarningsDetector(
			fmp,
			cfg.Signals.EarningsSurprise.MinSurpisePct,
			cfg.Signals.EarningsSurprise.LookbackDays,
		))
	}
	if shouldRun(signalFilter, "insider") && cfg.Signals.InsiderBuying.Enabled {
		detectors = append(detectors, signal.NewInsiderDetector(
			edgar,
			cfg.Signals.InsiderBuying.MinInsiders,
			cfg.Signals.InsiderBuying.LookbackDays,
			cfg.Signals.InsiderBuying.Exclude10b5_1,
		))
	}

	if len(detectors) == 0 {
		return fmt.Errorf("no signal detectors enabled — check config.yaml")
	}

	// ── Detect signals ─────────────────────────────────────────────────────
	fmt.Println("[2/4] Scanning signals...")
	ctx := context.Background()

	var allSignals []signal.Signal
	for _, d := range detectors {
		fmt.Printf("  → %s...", d.Name())
		sigs, err := d.Detect(ctx, tickers)
		if err != nil {
			fmt.Printf(" error: %v\n", err)
			continue
		}
		fmt.Printf(" %d found\n", len(sigs))
		allSignals = append(allSignals, sigs...)
	}

	deduped := signal.Deduplicate(allSignals)
	fmt.Printf("  → Deduplicated: %d unique candidates\n", len(deduped))

	if len(deduped) == 0 {
		fmt.Println("No signals detected today.")
		return nil
	}

	// Sort by strength descending
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Strength > deduped[j].Strength
	})

	// Cap at max_results
	if len(deduped) > cfg.Output.MaxResults {
		deduped = deduped[:cfg.Output.MaxResults]
	}

	// ── Enrich ────────────────────────────────────────────────────────────
	fmt.Printf("[3/4] Enriching %d candidates...\n", len(deduped))
	enricherInst := enricher.New(finnhub, finnhub, finnhub, edgar)

	enriched := make([]*enricher.EnrichedData, 0, len(deduped))
	for _, sig := range deduped {
		// Add sector from universe
		if e := uni.Find(sig.Ticker); e != nil && sig.Metadata != nil {
			sig.Metadata["sector"] = e.Sector
		}
		data, err := enricherInst.Enrich(ctx, sig)
		if err != nil {
			log.Printf("warn: enrich %s: %v", sig.Ticker, err)
			continue
		}
		// Set sector from universe if not from API
		if data.Fundamentals != nil && data.Fundamentals.Sector == "" {
			if e := uni.Find(sig.Ticker); e != nil {
				data.Fundamentals.Sector = e.Sector
			}
		}
		enriched = append(enriched, data)
	}

	// ── Analyse ───────────────────────────────────────────────────────────
	fmt.Printf("[4/4] Running AI analysis on %d candidates...\n", len(enriched))

	if cfg.APIKeys.Claude == "" || cfg.APIKeys.Claude == "your-anthropic-key-here" {
		return fmt.Errorf("Claude API key not configured in config.yaml")
	}

	ana := analyst.New(cfg.APIKeys.Claude, cfg.Claude.Model, cfg.Claude.MaxTokens, cfg.Claude.PromptTemplate)

	var analyses []*analyst.Analysis
	for _, data := range enriched {
		fmt.Printf("  → Analysing %s (%s)...", data.Signal.Ticker, data.Signal.Type)
		result, err := ana.Analyse(ctx, data)
		if err != nil {
			fmt.Printf(" error: %v\n", err)
			continue
		}
		fmt.Printf(" done (conviction: %d/10, verdict: %s)\n", result.ConvictionScore, result.Verdict)
		analyses = append(analyses, result)

		// Persist to DB
		if db != nil {
			sigID, _ := db.SaveSignal(data.Signal)
			_ = db.SaveAnalysis(result, sigID)
		}
	}

	// ── Output ────────────────────────────────────────────────────────────
	output.RenderTable(analyses, cfg.Output.MinConviction)

	switch strings.ToLower(exportFormat) {
	case "json":
		path := fmt.Sprintf("huginn-%s.json", time.Now().Format("2006-01-02"))
		_ = output.ExportJSON(analyses, path)
	case "markdown", "md":
		path := fmt.Sprintf("huginn-%s.md", time.Now().Format("2006-01-02"))
		_ = output.ExportMarkdown(analyses, path)
	}

	return nil
}

// ── huginn detail <TICKER> ───────────────────────────────────────────────────

func newDetailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detail <TICKER>",
		Short: "Show full AI thesis for a ticker",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDetail(strings.ToUpper(args[0]))
		},
	}
}

func runDetail(ticker string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	uni, err := loadUniverse(cfg)
	if err != nil {
		return err
	}

	finnhub := source.NewFinnhub(cfg.APIKeys.Finnhub)
	edgar := source.NewEDAGAR("Huginn/1.0 github.com/tonyjoanes/huginn")
	enricherInst := enricher.New(finnhub, finnhub, finnhub, edgar)

	// Create a synthetic signal for the ticker
	sig := signal.Signal{
		Ticker:      ticker,
		Type:        "on-demand",
		Description: "Requested via huginn detail",
		Strength:    1.0,
		DetectedAt:  time.Now(),
		Metadata:    make(map[string]interface{}),
	}
	if e := uni.Find(ticker); e != nil {
		sig.Metadata["sector"] = e.Sector
	}

	fmt.Printf("Enriching %s...\n", ticker)
	ctx := context.Background()
	data, err := enricherInst.Enrich(ctx, sig)
	if err != nil {
		return fmt.Errorf("enrich %s: %w", ticker, err)
	}
	if data.Fundamentals != nil && data.Fundamentals.Sector == "" {
		if e := uni.Find(ticker); e != nil {
			data.Fundamentals.Sector = e.Sector
		}
	}

	if cfg.APIKeys.Claude == "" || cfg.APIKeys.Claude == "your-anthropic-key-here" {
		return fmt.Errorf("Claude API key not configured in config.yaml")
	}

	fmt.Printf("Analysing %s with Claude...\n", ticker)
	ana := analyst.New(cfg.APIKeys.Claude, cfg.Claude.Model, cfg.Claude.MaxTokens, cfg.Claude.PromptTemplate)
	result, err := ana.Analyse(ctx, data)
	if err != nil {
		return fmt.Errorf("analyse %s: %w", ticker, err)
	}

	output.PrintDetail(result)
	return nil
}

// ── huginn history ────────────────────────────────────────────────────────────

func newHistoryCmd() *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show past signals and analyses",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHistory(days)
		},
	}
	cmd.Flags().IntVarP(&days, "days", "d", 30, "number of days to look back")
	return cmd
}

func runHistory(days int) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	records, err := db.GetHistory(days)
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}

	if len(records) == 0 {
		fmt.Printf("No analyses in the last %d days.\n", days)
		return nil
	}

	fmt.Printf("\nLast %d days (%d records):\n\n", days, len(records))
	fmt.Printf("%-12s %-20s %-12s %-6s %-10s %-10s\n",
		"Date", "Ticker", "Signal", "Score", "Entry", "Verdict")
	fmt.Println(strings.Repeat("-", 75))

	for _, r := range records {
		entry := "—"
		if r.Entry > 0 {
			entry = fmt.Sprintf("$%.2f", r.Entry)
		}
		fmt.Printf("%-12s %-20s %-12s %-6d %-10s %-10s\n",
			r.CreatedAt.Format("2006-01-02"),
			r.Ticker,
			truncate(r.SignalType, 12),
			r.ConvictionScore,
			entry,
			r.Verdict,
		)
	}
	fmt.Println()
	return nil
}

// ── huginn add-ticker <TICKER> ────────────────────────────────────────────────

func newAddTickerCmd() *cobra.Command {
	var name, sector string
	cmd := &cobra.Command{
		Use:   "add-ticker <TICKER>",
		Short: "Add a ticker to the universe",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAddTicker(strings.ToUpper(args[0]), name, sector)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "company name")
	cmd.Flags().StringVar(&sector, "sector", "", "sector")
	return cmd
}

func runAddTicker(ticker, name, sector string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	uni, err := loadUniverse(cfg)
	if err != nil {
		return err
	}

	uni.Add(universe.TickerEntry{
		Ticker: ticker,
		Name:   name,
		Sector: sector,
	})

	if err := uni.SaveToFile(cfg.Universe.File); err != nil {
		return fmt.Errorf("save universe: %w", err)
	}

	fmt.Printf("Added %s to universe (%s tickers total).\n", ticker, func() string {
		return fmt.Sprintf("%d", len(uni.Tickers))
	}())
	return nil
}

// ── huginn test-prompt <TICKER> ───────────────────────────────────────────────

func newTestPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test-prompt <TICKER>",
		Short: "Print the Claude prompt for a ticker (no API call)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestPrompt(strings.ToUpper(args[0]))
		},
	}
}

func runTestPrompt(ticker string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	uni, err := loadUniverse(cfg)
	if err != nil {
		return err
	}

	finnhub := source.NewFinnhub(cfg.APIKeys.Finnhub)
	edgar := source.NewEDAGAR("Huginn/1.0 github.com/tonyjoanes/huginn")
	enricherInst := enricher.New(finnhub, finnhub, finnhub, edgar)

	sig := signal.Signal{
		Ticker:      ticker,
		Type:        "test",
		Description: "Test prompt generation",
		Strength:    1.0,
		DetectedAt:  time.Now(),
		Metadata:    make(map[string]interface{}),
	}
	if e := uni.Find(ticker); e != nil {
		sig.Metadata["sector"] = e.Sector
	}

	fmt.Printf("Enriching %s for prompt test...\n\n", ticker)
	ctx := context.Background()
	data, err := enricherInst.Enrich(ctx, sig)
	if err != nil {
		return fmt.Errorf("enrich %s: %w", ticker, err)
	}

	ana := analyst.New("", cfg.Claude.Model, cfg.Claude.MaxTokens, cfg.Claude.PromptTemplate)
	prompt, err := ana.BuildPromptOnly(data)
	if err != nil {
		return fmt.Errorf("build prompt: %w", err)
	}

	fmt.Println("══ Generated Prompt ══")
	fmt.Println(prompt)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func loadConfig() (*config.Config, error) {
	// Look for config in current dir or executable dir
	paths := []string{
		configPath,
		filepath.Join(execDir(), configPath),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			cfg, err := config.Load(p)
			if err != nil {
				return nil, fmt.Errorf("load config %s: %w", p, err)
			}
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("config file not found (tried: %s)", strings.Join(paths, ", "))
}

func loadUniverse(cfg *config.Config) (*universe.Universe, error) {
	paths := []string{
		cfg.Universe.File,
		filepath.Join(execDir(), cfg.Universe.File),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return universe.LoadFromFile(p)
		}
	}
	return nil, fmt.Errorf("universe file not found: %s", cfg.Universe.File)
}

func openStore(cfg *config.Config) (*store.Store, error) {
	return store.Open(cfg.Storage.Path)
}

func shouldRun(filter, name string) bool {
	if filter == "" {
		return true
	}
	return strings.EqualFold(filter, name)
}

func execDir() string {
	ex, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(ex)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
