package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	APIKeys  APIKeys       `mapstructure:"api_keys"`
	Claude   ClaudeConfig  `mapstructure:"claude"`
	Universe UniverseCfg   `mapstructure:"universe"`
	Signals  SignalsCfg    `mapstructure:"signals"`
	Output   OutputCfg     `mapstructure:"output"`
	Storage  StorageCfg    `mapstructure:"storage"`
}

type APIKeys struct {
	Finnhub      string `mapstructure:"finnhub"`
	FMP          string `mapstructure:"fmp"`
	AlphaVantage string `mapstructure:"alpha_vantage"`
	Claude       string `mapstructure:"claude"`
}

type ClaudeConfig struct {
	Model          string `mapstructure:"model"`
	MaxTokens      int    `mapstructure:"max_tokens"`
	PromptTemplate string `mapstructure:"prompt_template"`
}

type UniverseCfg struct {
	Source       string  `mapstructure:"source"`
	File         string  `mapstructure:"file"`
	MinMarketCap float64 `mapstructure:"min_market_cap"`
}

type SignalsCfg struct {
	VolumeAnomaly   VolumeAnomalyCfg   `mapstructure:"volume_anomaly"`
	EarningsSurprise EarningsSurpriseCfg `mapstructure:"earnings_surprise"`
	InsiderBuying   InsiderBuyingCfg   `mapstructure:"insider_buying"`
}

type VolumeAnomalyCfg struct {
	Enabled          bool    `mapstructure:"enabled"`
	VolumeMultiplier float64 `mapstructure:"volume_multiplier"`
	MinPriceChange   float64 `mapstructure:"min_price_change"`
}

type EarningsSurpriseCfg struct {
	Enabled        bool    `mapstructure:"enabled"`
	MinSurpisePct  float64 `mapstructure:"min_surprise_pct"`
	LookbackDays   int     `mapstructure:"lookback_days"`
}

type InsiderBuyingCfg struct {
	Enabled       bool `mapstructure:"enabled"`
	MinInsiders   int  `mapstructure:"min_insiders"`
	LookbackDays  int  `mapstructure:"lookback_days"`
	Exclude10b5_1 bool `mapstructure:"exclude_10b5_1"`
}

type OutputCfg struct {
	Format        string `mapstructure:"format"`
	MaxResults    int    `mapstructure:"max_results"`
	MinConviction int    `mapstructure:"min_conviction"`
}

type StorageCfg struct {
	Path string `mapstructure:"path"`
}

// Load reads config.yaml from the given path and returns a Config.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.Storage.Path = expandHome(cfg.Storage.Path)

	if cfg.Claude.Model == "" {
		cfg.Claude.Model = "claude-sonnet-4-20250514"
	}
	if cfg.Claude.MaxTokens == 0 {
		cfg.Claude.MaxTokens = 2000
	}
	if cfg.Claude.PromptTemplate == "" {
		cfg.Claude.PromptTemplate = "prompts/analysis.tmpl"
	}
	if cfg.Output.MaxResults == 0 {
		cfg.Output.MaxResults = 20
	}
	if cfg.Output.MinConviction == 0 {
		cfg.Output.MinConviction = 5
	}
	if cfg.Signals.VolumeAnomaly.VolumeMultiplier == 0 {
		cfg.Signals.VolumeAnomaly.VolumeMultiplier = 2.5
	}
	if cfg.Signals.VolumeAnomaly.MinPriceChange == 0 {
		cfg.Signals.VolumeAnomaly.MinPriceChange = 0.02
	}
	if cfg.Signals.InsiderBuying.MinInsiders == 0 {
		cfg.Signals.InsiderBuying.MinInsiders = 2
	}
	if cfg.Signals.InsiderBuying.LookbackDays == 0 {
		cfg.Signals.InsiderBuying.LookbackDays = 30
	}
	if cfg.Signals.EarningsSurprise.MinSurpisePct == 0 {
		cfg.Signals.EarningsSurprise.MinSurpisePct = 10.0
	}
	if cfg.Signals.EarningsSurprise.LookbackDays == 0 {
		cfg.Signals.EarningsSurprise.LookbackDays = 7
	}

	return &cfg, nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
