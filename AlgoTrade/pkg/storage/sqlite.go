package storage

import (
	"database/sql"
	"fmt"
	"time"

	"AlgoTrade/pkg/math"
	_ "modernc.org/sqlite"
)

type SQLiteStorage struct {
	db *sql.DB
}

func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	return &SQLiteStorage{db: db}, nil
}

func (s *SQLiteStorage) InitializeSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS experiments (
			id TEXT PRIMARY KEY,
			description TEXT,
			strategy_type TEXT,
			ticker_a TEXT,
			ticker_b TEXT,
			macro_ticker TEXT,
			window_size INTEGER,
			max_lag INTEGER,
			created_at TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS results (
			experiment_id TEXT,
			timestamp TIMESTAMP,
			optimal_lag INTEGER,
			correlation REAL,
			volatility REAL,
			is_stable INTEGER,
			t_statistic REAL,
			p_value REAL,
			is_significant INTEGER,
			FOREIGN KEY(experiment_id) REFERENCES experiments(id)
		);`,
		`CREATE TABLE IF NOT EXISTS second_order_correlations (
			experiment_id TEXT PRIMARY KEY,
			macro_ticker TEXT,
			correlation REAL,
			run_date TIMESTAMP,
			FOREIGN KEY(experiment_id) REFERENCES experiments(id)
		);`,
		`CREATE TABLE IF NOT EXISTS discovered_edges (
			ticker_a TEXT,
			ticker_b TEXT,
			macro_ticker TEXT,
			survival_rate REAL,
			dominant_lag INTEGER,
			avg_correlation REAL,
			discovered_at TIMESTAMP,
			PRIMARY KEY (ticker_a, ticker_b, macro_ticker)
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed executing schema migration: %w", err)
		}
	}

	// Dynamic schema upgrades: add new columns if they do not exist in existing database files
	s.db.Exec("ALTER TABLE results ADD COLUMN t_statistic REAL")
	s.db.Exec("ALTER TABLE results ADD COLUMN p_value REAL")
	s.db.Exec("ALTER TABLE results ADD COLUMN is_significant INTEGER")

	return nil
}

func (s *SQLiteStorage) SaveExperiment(exp ExperimentRecord) error {
	q := `INSERT OR REPLACE INTO experiments (id, description, strategy_type, ticker_a, ticker_b, macro_ticker, window_size, max_lag, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q, exp.ID, exp.Description, exp.StrategyType, exp.TickerA, exp.TickerB, exp.MacroTicker, exp.WindowSize, exp.MaxLag, exp.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert experiment: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) SaveResults(experimentID string, results []math.SweetSpotResult, timestamps []time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear out any old rolling results for this experiment to prevent duplicate rows
	_, err = tx.Exec("DELETE FROM results WHERE experiment_id = ?", experimentID)
	if err != nil {
		return fmt.Errorf("failed to clear existing results: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO results (experiment_id, timestamp, optimal_lag, correlation, volatility, is_stable, t_statistic, p_value, is_significant)
	                         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, res := range results {
		// Map index to the exact timestamp of the window end
		var t time.Time
		if res.WindowEndIndex-1 < len(timestamps) {
			t = timestamps[res.WindowEndIndex-1]
		} else if len(timestamps) > 0 {
			t = timestamps[len(timestamps)-1]
		} else {
			t = time.Now()
		}

		isStableInt := 0
		if res.IsStable {
			isStableInt = 1
		}

		isSigInt := 0
		if res.IsSignificant {
			isSigInt = 1
		}

		_, err = stmt.Exec(experimentID, t, res.OptimalLag, res.Correlation, res.Volatility, isStableInt, res.TStatistic, res.PValue, isSigInt)
		if err != nil {
			return fmt.Errorf("failed to execute result insert: %w", err)
		}
	}

	return tx.Commit()
}

func (s *SQLiteStorage) SaveSecondOrderCorrelation(experimentID string, macroTicker string, correlation float64) error {
	q := `INSERT OR REPLACE INTO second_order_correlations (experiment_id, macro_ticker, correlation, run_date)
	      VALUES (?, ?, ?, ?)`
	_, err := s.db.Exec(q, experimentID, macroTicker, correlation, time.Now())
	if err != nil {
		return fmt.Errorf("failed to save second order correlation: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetExperiments() ([]ExperimentRecord, error) {
	rows, err := s.db.Query(`SELECT id, description, strategy_type, ticker_a, ticker_b, macro_ticker, window_size, max_lag, created_at 
	                         FROM experiments ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed query experiments: %w", err)
	}
	defer rows.Close()

	var records []ExperimentRecord
	for rows.Next() {
		var rec ExperimentRecord
		err := rows.Scan(&rec.ID, &rec.Description, &rec.StrategyType, &rec.TickerA, &rec.TickerB, &rec.MacroTicker, &rec.WindowSize, &rec.MaxLag, &rec.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed scanning experiment: %w", err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *SQLiteStorage) GetExperimentResults(experimentID string) ([]ResultRecord, error) {
	rows, err := s.db.Query(`SELECT experiment_id, timestamp, optimal_lag, correlation, volatility, is_stable, t_statistic, p_value, is_significant 
	                         FROM results WHERE experiment_id = ? ORDER BY timestamp ASC`, experimentID)
	if err != nil {
		return nil, fmt.Errorf("failed query results: %w", err)
	}
	defer rows.Close()

	var records []ResultRecord
	for rows.Next() {
		var rec ResultRecord
		var isStableInt int
		var isSigInt int
		err := rows.Scan(&rec.ExperimentID, &rec.Timestamp, &rec.OptimalLag, &rec.Correlation, &rec.Volatility, &isStableInt, &rec.TStatistic, &rec.PValue, &isSigInt)
		if err != nil {
			return nil, fmt.Errorf("failed scanning result: %w", err)
		}
		rec.IsStable = isStableInt == 1
		rec.IsSignificant = isSigInt == 1
		records = append(records, rec)
	}
	return records, nil
}

func (s *SQLiteStorage) GetSecondOrderCorrelation(experimentID string) (float64, error) {
	var corr float64
	err := s.db.QueryRow(`SELECT correlation FROM second_order_correlations WHERE experiment_id = ?`, experimentID).Scan(&corr)
	if err == sql.ErrNoRows {
		return 0.0, nil
	} else if err != nil {
		return 0.0, fmt.Errorf("failed fetching second order correlation: %w", err)
	}
	return corr, nil
}

func (s *SQLiteStorage) SaveDiscoveredEdge(edge DiscoveredEdge) error {
	q := `INSERT OR REPLACE INTO discovered_edges (ticker_a, ticker_b, macro_ticker, survival_rate, dominant_lag, avg_correlation, discovered_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q, edge.TickerA, edge.TickerB, edge.MacroTicker, edge.SurvivalRate, edge.DominantLag, edge.AvgCorrelation, edge.DiscoveredAt)
	if err != nil {
		return fmt.Errorf("failed to save discovered edge: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) GetDiscoveredEdges() ([]DiscoveredEdge, error) {
	rows, err := s.db.Query(`SELECT ticker_a, ticker_b, macro_ticker, survival_rate, dominant_lag, avg_correlation, discovered_at 
	                         FROM discovered_edges ORDER BY survival_rate DESC, avg_correlation DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query discovered edges: %w", err)
	}
	defer rows.Close()

	var records []DiscoveredEdge
	for rows.Next() {
		var rec DiscoveredEdge
		err := rows.Scan(&rec.TickerA, &rec.TickerB, &rec.MacroTicker, &rec.SurvivalRate, &rec.DominantLag, &rec.AvgCorrelation, &rec.DiscoveredAt)
		if err != nil {
			return nil, fmt.Errorf("failed scanning discovered edge: %w", err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}
