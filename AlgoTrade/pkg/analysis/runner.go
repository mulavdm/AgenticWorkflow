package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	"AlgoTrade/pkg/ingest"
	"AlgoTrade/pkg/math"
	"AlgoTrade/pkg/storage"
)

// Manifest represents the JSON configuration structure for reproducing an experiment.
type Manifest struct {
	ExperimentID        string  `json:"experiment_id"`
	Description         string  `json:"description"`
	StrategyType        string  `json:"strategy_type"`
	TickerA             string  `json:"ticker_a"`
	TickerB             string  `json:"ticker_b"`
	MacroTicker         string  `json:"macro_ticker"`
	RollingWindowHours  int     `json:"rolling_window_hours"`
	StepSize            int     `json:"step_size"`
	MaxLagTested        int     `json:"max_lag_tested"`
	VolatilityThreshold float64 `json:"volatility_threshold"`
}

// Runner coordinates the quantitative strategy execution lifecycle
type Runner struct {
	dataDir string
}

func NewRunner(dataDir string) *Runner {
	return &Runner{dataDir: dataDir}
}

func (r *Runner) Name() string {
	return "LaggedCrossCorrelationRunner"
}

// Run loads a strategy manifest, parses raw stock/macro CSVs, aligns them, calculates rolling lags,
// runs second-order correlation, and logs the entire state into our pluggable database.
func (r *Runner) Run(ctx context.Context, db storage.Storage, manifestPath string) error {
	// 1. Read Strategy Manifest JSON
	manifestBytes, err := ioutil.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed reading strategy config: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("failed parsing strategy JSON config: %w", err)
	}

	fmt.Printf("[Runner] Launching Experiment: %s (%s)\n", m.ExperimentID, m.Description)
	fmt.Printf("[Runner] Asset A: %s | Asset B: %s | Macro: %s\n", m.TickerA, m.TickerB, m.MacroTicker)

	// 2. Load CSV files
	aPath := filepath.Join(r.dataDir, m.TickerA+".csv")
	bPath := filepath.Join(r.dataDir, m.TickerB+".csv")
	mPath := filepath.Join(r.dataDir, m.MacroTicker+".csv")

	barsA, err := ingest.LoadCSV(aPath)
	if err != nil {
		return fmt.Errorf("failed loading asset A data: %w", err)
	}
	barsB, err := ingest.LoadCSV(bPath)
	if err != nil {
		return fmt.Errorf("failed loading asset B data: %w", err)
	}
	barsMacro, err := ingest.LoadCSV(mPath)
	if err != nil {
		return fmt.Errorf("failed loading macro data: %w", err)
	}

	// 3. Align primary Assets A and B
	alignedA, alignedB := ingest.AlignStockBars(barsA, barsB)
	if len(alignedA) < m.RollingWindowHours {
		return fmt.Errorf("aligned asset A and B series has insufficient length (%d) for window size %d", len(alignedA), m.RollingWindowHours)
	}

	// 4. Calculate Percentage Returns
	returnsA := ingest.CalculateReturns(alignedA)
	returnsB := ingest.CalculateReturns(alignedB)

	// Keep track of timestamps associated with returns.
	// Since returns represent i to i+1, return[i] timestamp matches alignedA[i+1].Timestamp
	returnTimestamps := make([]time.Time, len(returnsA))
	for i := 0; i < len(returnsA); i++ {
		returnTimestamps[i] = alignedA[i+1].Timestamp
	}

	// 5. Execute First-Order math: Rolling Sweet Spot cross-correlation
	rollingResults := math.RollingSweetSpot(
		returnsA,
		returnsB,
		m.RollingWindowHours,
		m.StepSize,
		m.MaxLagTested,
		m.VolatilityThreshold,
	)

	if len(rollingResults) == 0 {
		return fmt.Errorf("rolling sweet spot calculation yielded empty results")
	}

	// 6. Execute Second-Order Correlation
	// Calculate Macro returns to align with our sweet spots
	macroReturns := ingest.CalculateReturns(barsMacro)
	macroPoints := make([]ingest.MacroPoint, len(macroReturns))
	for i := 0; i < len(macroReturns); i++ {
		macroPoints[i] = ingest.MacroPoint{
			Timestamp: barsMacro[i+1].Timestamp,
			Value:     macroReturns[i],
		}
	}

	// Convert sweet spot results into unified StockBars so we can leverage the aligner
	sweetSpotBars := make([]ingest.StockBar, len(rollingResults))
	for i, res := range rollingResults {
		// Map index back to the exact timestamp of the return timeline
		t := returnTimestamps[res.WindowEndIndex-1]
		sweetSpotBars[i] = ingest.StockBar{
			Timestamp: t,
			Close:     float64(res.OptimalLag), // Put lag inside the close field to reuse the Aligner!
		}
	}

	// Align calculated Sweet Spots and Macro Returns
	alignedLags, alignedMacros := ingest.AlignWithMacro(sweetSpotBars, macroPoints)

	// Compute Pearson second-order correlation
	secondOrderCorr := 0.0
	if len(alignedLags) > 0 {
		lagVals := make([]float64, len(alignedLags))
		macroVals := make([]float64, len(alignedMacros))
		for i := 0; i < len(alignedLags); i++ {
			lagVals[i] = alignedLags[i].Close
			macroVals[i] = alignedMacros[i].Value
		}

		// Calculate Pearson correlation of Sweet Spot Lag vs Macro Returns
		var err error
		secondOrderCorr, err = math.LaggedCorrelation(lagVals, macroVals, 0)
		if err != nil {
			secondOrderCorr = 0.0
		}
	}

	fmt.Printf("[Runner] Analysis complete. Rolling Results: %d windows. Second-Order Macro Correlation: %.4f\n", len(rollingResults), secondOrderCorr)

	// 7. Save to pluggable SQL Storage Ledger
	record := storage.ExperimentRecord{
		ID:           m.ExperimentID,
		Description:  m.Description,
		StrategyType: m.StrategyType,
		TickerA:      m.TickerA,
		TickerB:      m.TickerB,
		MacroTicker:  m.MacroTicker,
		WindowSize:   m.RollingWindowHours,
		MaxLag:       m.MaxLagTested,
		CreatedAt:    time.Now().UTC(),
	}

	if err := db.SaveExperiment(record); err != nil {
		return fmt.Errorf("failed saving experiment metadata to database: %w", err)
	}

	if err := db.SaveResults(m.ExperimentID, rollingResults, returnTimestamps); err != nil {
		return fmt.Errorf("failed saving rolling results to database: %w", err)
	}

	if err := db.SaveSecondOrderCorrelation(m.ExperimentID, m.MacroTicker, secondOrderCorr); err != nil {
		return fmt.Errorf("failed saving second order correlation metrics to database: %w", err)
	}

	fmt.Printf("[Runner] Successfully logged experiment results to the storage ledger.\n")
	return nil
}
