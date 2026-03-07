package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/tonyjoanes/huginn/internal/analyst"
	"github.com/tonyjoanes/huginn/internal/signal"
)

// Store persists signals and analyses to a local SQLite database.
type Store struct {
	db *sql.DB
}

// AnalysisRecord is a stored analysis record for history display.
type AnalysisRecord struct {
	ID              int64
	Ticker          string
	SignalType       string
	ConvictionScore int
	Verdict         string
	Thesis          string
	Entry           float64
	StopLoss        float64
	Target1         float64
	CreatedAt       time.Time
}

// Open opens (or creates) the SQLite database at the given path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("store: mkdir %s: %w", filepath.Dir(path), err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticker TEXT NOT NULL,
			signal_type TEXT NOT NULL,
			description TEXT,
			strength REAL,
			detected_at DATETIME,
			metadata TEXT
		);

		CREATE TABLE IF NOT EXISTS analyses (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticker TEXT NOT NULL,
			signal_id INTEGER,
			signal_type TEXT,
			conviction_score INTEGER,
			verdict TEXT,
			thesis TEXT,
			trade_plan TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(signal_id) REFERENCES signals(id)
		);

		CREATE TABLE IF NOT EXISTS universe (
			ticker TEXT PRIMARY KEY,
			name TEXT,
			sector TEXT,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			active INTEGER DEFAULT 1
		);
	`)
	return err
}

// SaveSignal persists a signal and returns its row ID.
func (s *Store) SaveSignal(sig signal.Signal) (int64, error) {
	meta, _ := json.Marshal(sig.Metadata)
	res, err := s.db.Exec(
		`INSERT INTO signals (ticker, signal_type, description, strength, detected_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sig.Ticker, sig.Type, sig.Description, sig.Strength, sig.DetectedAt, string(meta),
	)
	if err != nil {
		return 0, fmt.Errorf("store: save signal %s: %w", sig.Ticker, err)
	}
	return res.LastInsertId()
}

// SaveAnalysis persists a Claude analysis linked to a signal.
func (s *Store) SaveAnalysis(a *analyst.Analysis, signalID int64) error {
	tradePlan, _ := json.Marshal(a.TradePlan)
	_, err := s.db.Exec(
		`INSERT INTO analyses (ticker, signal_id, signal_type, conviction_score, verdict, thesis, trade_plan)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.Ticker, signalID, a.SignalType, a.ConvictionScore, a.Verdict, a.Thesis, string(tradePlan),
	)
	if err != nil {
		return fmt.Errorf("store: save analysis %s: %w", a.Ticker, err)
	}
	return nil
}

// GetHistory returns analyses from the last `days` days.
func (s *Store) GetHistory(days int) ([]AnalysisRecord, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	rows, err := s.db.Query(
		`SELECT a.id, a.ticker, a.signal_type, a.conviction_score, a.verdict, a.thesis,
		        a.trade_plan, a.created_at
		 FROM analyses a
		 WHERE a.created_at >= ?
		 ORDER BY a.created_at DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get history: %w", err)
	}
	defer rows.Close()

	var records []AnalysisRecord
	for rows.Next() {
		var r AnalysisRecord
		var tradePlanJSON string
		var createdAt string
		err := rows.Scan(&r.ID, &r.Ticker, &r.SignalType, &r.ConvictionScore,
			&r.Verdict, &r.Thesis, &tradePlanJSON, &createdAt)
		if err != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			r.CreatedAt = t
		} else if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			r.CreatedAt = t
		}

		var tp analyst.TradePlan
		if err := json.Unmarshal([]byte(tradePlanJSON), &tp); err == nil {
			r.Entry = tp.Entry
			r.StopLoss = tp.StopLoss
			r.Target1 = tp.Target1
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// AddTicker adds a ticker to the universe table.
func (s *Store) AddTicker(ticker, name, sector string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO universe (ticker, name, sector) VALUES (?, ?, ?)`,
		ticker, name, sector,
	)
	return err
}

// GetActiveTickers returns all active tickers from the universe table.
func (s *Store) GetActiveTickers() ([]string, error) {
	rows, err := s.db.Query(`SELECT ticker FROM universe WHERE active = 1 ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tickers = append(tickers, t)
		}
	}
	return tickers, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}
