package storage

import (
	"database/sql"
	"fmt"
	"time"

	"AlgoTrade/pkg/math"
	_ "github.com/lib/pq"
)

type PostgresStorage struct {
	db *sql.DB
}

func NewPostgresStorage(connStr string) (*PostgresStorage, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping postgres database: %w", err)
	}

	return &PostgresStorage{db: db}, nil
}

func (p *PostgresStorage) InitializeSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS experiments (
			id VARCHAR(255) PRIMARY KEY,
			description TEXT,
			strategy_type VARCHAR(255),
			ticker_a VARCHAR(50),
			ticker_b VARCHAR(50),
			macro_ticker VARCHAR(50),
			window_size INTEGER,
			max_lag INTEGER,
			created_at TIMESTAMP WITH TIME ZONE
		);`,
		`CREATE TABLE IF NOT EXISTS results (
			experiment_id VARCHAR(255) REFERENCES experiments(id),
			timestamp TIMESTAMP WITH TIME ZONE,
			optimal_lag INTEGER,
			correlation DOUBLE PRECISION,
			volatility DOUBLE PRECISION,
			is_stable BOOLEAN,
			t_statistic DOUBLE PRECISION,
			p_value DOUBLE PRECISION,
			is_significant BOOLEAN
		);`,
		`CREATE TABLE IF NOT EXISTS second_order_correlations (
			experiment_id VARCHAR(255) PRIMARY KEY REFERENCES experiments(id),
			macro_ticker VARCHAR(50),
			correlation DOUBLE PRECISION,
			run_date TIMESTAMP WITH TIME ZONE
		);`,
		`CREATE TABLE IF NOT EXISTS discovered_edges (
			ticker_a VARCHAR(50),
			ticker_b VARCHAR(50),
			macro_ticker VARCHAR(50),
			survival_rate DOUBLE PRECISION,
			dominant_lag INTEGER,
			avg_correlation DOUBLE PRECISION,
			discovered_at TIMESTAMP WITH TIME ZONE,
			PRIMARY KEY (ticker_a, ticker_b, macro_ticker)
		);`,
	}

	// Try to enable the TimescaleDB extension (cascades and creates schema if needed)
	p.db.Exec("CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;")

	for _, q := range queries {
		if _, err := p.db.Exec(q); err != nil {
			return fmt.Errorf("failed executing postgres schema query: %w", err)
		}
	}

	// Try to convert results to a TimescaleDB hypertable (partitioned by timestamp)
	// We use standard error recovery: if the TimescaleDB function doesn't exist, we fall back to index.
	_, errHyper := p.db.Exec("SELECT create_hypertable('results', 'timestamp', if_not_exists => TRUE);")
	if errHyper != nil {
		fmt.Printf("[Postgres] TimescaleDB hypertable conversion skipped (not available or standard Postgres). Falling back to standard indexes.\n")
		p.db.Exec("CREATE INDEX IF NOT EXISTS idx_results_timestamp ON results(timestamp);")
	} else {
		fmt.Println("[Postgres] Successfully converted 'results' table into a partitioned TimescaleDB Hypertable!")
	}

	// dynamic upgrade columns for existing databases
	p.db.Exec("ALTER TABLE results ADD COLUMN t_statistic DOUBLE PRECISION")
	p.db.Exec("ALTER TABLE results ADD COLUMN p_value DOUBLE PRECISION")
	p.db.Exec("ALTER TABLE results ADD COLUMN is_significant BOOLEAN")

	return nil
}

func (p *PostgresStorage) SaveExperiment(exp ExperimentRecord) error {
	q := `INSERT INTO experiments (id, description, strategy_type, ticker_a, ticker_b, macro_ticker, window_size, max_lag, created_at)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	      ON CONFLICT (id) DO UPDATE SET description = EXCLUDED.description, created_at = EXCLUDED.created_at`
	_, err := p.db.Exec(q, exp.ID, exp.Description, exp.StrategyType, exp.TickerA, exp.TickerB, exp.MacroTicker, exp.WindowSize, exp.MaxLag, exp.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert postgres experiment: %w", err)
	}
	return nil
}

func (p *PostgresStorage) SaveResults(experimentID string, results []math.SweetSpotResult, timestamps []time.Time) error {
	tx, err := p.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Clear out any old rolling results for this experiment to prevent duplicate rows
	_, err = tx.Exec("DELETE FROM results WHERE experiment_id = $1", experimentID)
	if err != nil {
		return fmt.Errorf("failed to clear existing results: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO results (experiment_id, timestamp, optimal_lag, correlation, volatility, is_stable, t_statistic, p_value, is_significant)
	                         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, res := range results {
		var t time.Time
		if res.WindowEndIndex-1 < len(timestamps) {
			t = timestamps[res.WindowEndIndex-1]
		} else if len(timestamps) > 0 {
			t = timestamps[len(timestamps)-1]
		} else {
			t = time.Now()
		}

		_, err = stmt.Exec(experimentID, t, res.OptimalLag, res.Correlation, res.Volatility, res.IsStable, res.TStatistic, res.PValue, res.IsSignificant)
		if err != nil {
			return fmt.Errorf("failed to insert result record: %w", err)
		}
	}

	return tx.Commit()
}

func (p *PostgresStorage) SaveSecondOrderCorrelation(experimentID string, macroTicker string, correlation float64) error {
	q := `INSERT INTO second_order_correlations (experiment_id, macro_ticker, correlation, run_date)
	      VALUES ($1, $2, $3, $4)
	      ON CONFLICT (experiment_id) DO UPDATE SET correlation = EXCLUDED.correlation, run_date = EXCLUDED.run_date`
	_, err := p.db.Exec(q, experimentID, macroTicker, correlation, time.Now())
	if err != nil {
		return fmt.Errorf("failed to save second order correlation: %w", err)
	}
	return nil
}

func (p *PostgresStorage) GetExperiments() ([]ExperimentRecord, error) {
	rows, err := p.db.Query(`SELECT id, description, strategy_type, ticker_a, ticker_b, macro_ticker, window_size, max_lag, created_at 
	                         FROM experiments ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query experiments: %w", err)
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

func (p *PostgresStorage) GetExperimentResults(experimentID string) ([]ResultRecord, error) {
	rows, err := p.db.Query(`SELECT experiment_id, timestamp, optimal_lag, correlation, volatility, is_stable, t_statistic, p_value, is_significant 
	                         FROM results WHERE experiment_id = $1 ORDER BY timestamp ASC`, experimentID)
	if err != nil {
		return nil, fmt.Errorf("failed to query results: %w", err)
	}
	defer rows.Close()

	var records []ResultRecord
	for rows.Next() {
		var rec ResultRecord
		err := rows.Scan(&rec.ExperimentID, &rec.Timestamp, &rec.OptimalLag, &rec.Correlation, &rec.Volatility, &rec.IsStable, &rec.TStatistic, &rec.PValue, &rec.IsSignificant)
		if err != nil {
			return nil, fmt.Errorf("failed scanning result: %w", err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func (p *PostgresStorage) GetSecondOrderCorrelation(experimentID string) (float64, error) {
	var corr float64
	err := p.db.QueryRow(`SELECT correlation FROM second_order_correlations WHERE experiment_id = $1`, experimentID).Scan(&corr)
	if err == sql.ErrNoRows {
		return 0.0, nil
	} else if err != nil {
		return 0.0, fmt.Errorf("failed to scan second order correlation: %w", err)
	}
	return corr, nil
}

func (p *PostgresStorage) SaveDiscoveredEdge(edge DiscoveredEdge) error {
	q := `INSERT INTO discovered_edges (ticker_a, ticker_b, macro_ticker, survival_rate, dominant_lag, avg_correlation, discovered_at)
	      VALUES ($1, $2, $3, $4, $5, $6, $7)
	      ON CONFLICT (ticker_a, ticker_b, macro_ticker) DO UPDATE 
	      SET survival_rate = EXCLUDED.survival_rate, dominant_lag = EXCLUDED.dominant_lag, 
	          avg_correlation = EXCLUDED.avg_correlation, discovered_at = EXCLUDED.discovered_at`
	_, err := p.db.Exec(q, edge.TickerA, edge.TickerB, edge.MacroTicker, edge.SurvivalRate, edge.DominantLag, edge.AvgCorrelation, edge.DiscoveredAt)
	if err != nil {
		return fmt.Errorf("failed to save discovered edge in postgres: %w", err)
	}
	return nil
}

func (p *PostgresStorage) GetDiscoveredEdges() ([]DiscoveredEdge, error) {
	rows, err := p.db.Query(`SELECT ticker_a, ticker_b, macro_ticker, survival_rate, dominant_lag, avg_correlation, discovered_at 
	                         FROM discovered_edges ORDER BY survival_rate DESC, avg_correlation DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query discovered edges in postgres: %w", err)
	}
	defer rows.Close()

	var records []DiscoveredEdge
	for rows.Next() {
		var rec DiscoveredEdge
		err := rows.Scan(&rec.TickerA, &rec.TickerB, &rec.MacroTicker, &rec.SurvivalRate, &rec.DominantLag, &rec.AvgCorrelation, &rec.DiscoveredAt)
		if err != nil {
			return nil, fmt.Errorf("failed scanning discovered edge in postgres: %w", err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func (p *PostgresStorage) Close() error {
	return p.db.Close()
}
