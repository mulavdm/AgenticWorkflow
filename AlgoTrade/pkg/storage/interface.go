package storage

import (
	"time"
	"AlgoTrade/pkg/math"
)

// ExperimentRecord stores details about an analytical hypothesis execution.
type ExperimentRecord struct {
	ID           string    `json:"id"`
	Description  string    `json:"description"`
	StrategyType string    `json:"strategy_type"`
	TickerA      string    `json:"ticker_a"`
	TickerB      string    `json:"ticker_b"`
	MacroTicker  string    `json:"macro_ticker"`
	WindowSize   int       `json:"window_size"`
	MaxLag       int       `json:"max_lag"`
	CreatedAt    time.Time `json:"created_at"`
}

// ResultRecord holds a single sliding window result record from the SQLite/PostgreSQL store.
type ResultRecord struct {
	ExperimentID  string    `json:"experiment_id"`
	Timestamp     time.Time `json:"timestamp"`
	OptimalLag    int       `json:"optimal_lag"`
	Correlation   float64   `json:"correlation"`
	Volatility    float64   `json:"volatility"`
	IsStable      bool      `json:"is_stable"`
	TStatistic    float64   `json:"t_statistic"`
	PValue        float64   `json:"p_value"`
	IsSignificant bool      `json:"is_significant"`
}

// DiscoveredEdge represents a lead-lag relationship that successfully survived Popperian falsification filters.
type DiscoveredEdge struct {
	TickerA        string    `json:"ticker_a"`
	TickerB        string    `json:"ticker_b"`
	MacroTicker    string    `json:"macro_ticker"`
	SurvivalRate   float64   `json:"survival_rate"`
	DominantLag    int       `json:"dominant_lag"`
	AvgCorrelation float64   `json:"avg_correlation"`
	DiscoveredAt   time.Time `json:"discovered_at"`
}

// Storage represents a pluggable storage driver for experiment ledgers.
type Storage interface {
	InitializeSchema() error
	SaveExperiment(exp ExperimentRecord) error
	SaveResults(experimentID string, results []math.SweetSpotResult, timestamps []time.Time) error
	SaveSecondOrderCorrelation(experimentID string, macroTicker string, correlation float64) error
	SaveDiscoveredEdge(edge DiscoveredEdge) error
	GetDiscoveredEdges() ([]DiscoveredEdge, error)
	GetExperiments() ([]ExperimentRecord, error)
	GetExperimentResults(experimentID string) ([]ResultRecord, error)
	GetSecondOrderCorrelation(experimentID string) (float64, error)
	Close() error
}
