package analysis

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	"AlgoTrade/pkg/ingest"
	stdmath "AlgoTrade/pkg/math"
	"AlgoTrade/pkg/storage"
)

// PopperianScanner automates lead-lag discovery across the asset universe
type PopperianScanner struct {
	dataDir string
}

func NewPopperianScanner(dataDir string) *PopperianScanner {
	return &PopperianScanner{dataDir: dataDir}
}

// Scan crawls the data directory, executes rolling p-value tests on all pairs,
// and persists any lead-lag edges that survive the strict survival threshold.
func (s *PopperianScanner) Scan(ctx context.Context, db storage.Storage, survivalThreshold float64) error {
	// 1. Locate all CSV files
	files, err := ioutil.ReadDir(s.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	var tickers []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(strings.ToLower(f.Name()), ".csv") {
			ticker := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			tickers = append(tickers, ticker)
		}
	}

	if len(tickers) < 2 {
		return fmt.Errorf("insufficient CSV files in data folder (minimum 2 needed, got %d)", len(tickers))
	}

	fmt.Printf("[Scanner] Starting Popperian scan across %d tickers (Survival Threshold: %.1f%%)\n", len(tickers), survivalThreshold*100)
	fmt.Printf("[Scanner] Target Universe: %s\n", strings.Join(tickers, ", "))
	fmt.Println("================================================================================")

	discoveredCount := 0
	scannedCount := 0

	// 2. Loop over all permutations (A vs B)
	for i := 0; i < len(tickers); i++ {
		for j := 0; j < len(tickers); j++ {
			if i == j {
				continue // Skip self-correlation
			}

			tickerA := tickers[i]
			tickerB := tickers[j]

			// Skip macro files if we want, but letting them run yields great comparative stats
			scannedCount++

			// Select distinct macroeconomic indicator
			macro := "SPY"
			if tickerA == "SPY" || tickerB == "SPY" {
				macro = "TLT" // Fallback to bond yields
			}
			if (tickerA == "SPY" && tickerB == "TLT") || (tickerA == "TLT" && tickerB == "SPY") {
				macro = "USO" // Fallback to oil
			}

			// Run backtest in-memory
			err := s.evaluatePair(db, tickerA, tickerB, macro, survivalThreshold, &discoveredCount)
			if err != nil {
				// Log warnings and keep scanning other pairs (fault tolerance)
				fmt.Printf("[Scanner] Skipping pair %s ➔ %s due to data bounds: %v\n", tickerA, tickerB, err)
			}
		}
	}

	fmt.Println("================================================================================")
	fmt.Printf("[Scanner] Scan Complete. Scanned %d pairs. Discovered %d robust lead-lag edges.\n", scannedCount, discoveredCount)
	return nil
}

func (s *PopperianScanner) evaluatePair(db storage.Storage, tickerA, tickerB, macro string, threshold float64, discoveredCount *int) error {
	aPath := filepath.Join(s.dataDir, tickerA+".csv")
	bPath := filepath.Join(s.dataDir, tickerB+".csv")

	barsA, err := ingest.LoadCSV(aPath)
	if err != nil {
		return err
	}
	barsB, err := ingest.LoadCSV(bPath)
	if err != nil {
		return err
	}

	// Align
	alignedA, alignedB := ingest.AlignStockBars(barsA, barsB)
	windowSize := 60
	if len(alignedA) < windowSize {
		return fmt.Errorf("aligned length (%d) too short", len(alignedA))
	}

	returnsA := ingest.CalculateReturns(alignedA)
	returnsB := ingest.CalculateReturns(alignedB)

	// Rolling hypothesis calculations
	maxLag := 10
	stepSize := 5
	volThreshold := 0.05 // default stability alert

	rollingResults := stdmath.RollingSweetSpot(
		returnsA,
		returnsB,
		windowSize,
		stepSize,
		maxLag,
		volThreshold,
	)

	if len(rollingResults) == 0 {
		return fmt.Errorf("rolling sweet spot returned empty results")
	}

	// Calculate Popperian metrics
	totalWindows := len(rollingResults)
	stableAndSigCount := 0
	var sumSignificantCorr float64
	lagFreqs := make(map[int]int)

	for _, res := range rollingResults {
		if res.IsStable && res.IsSignificant {
			stableAndSigCount++
			sumSignificantCorr += res.Correlation
			lagFreqs[res.OptimalLag]++
		}
	}

	survivalRate := float64(stableAndSigCount) / float64(totalWindows)

	// Determine if the relationship survives falsification
	if survivalRate >= threshold {
		// Calculate dominant lag (mode)
		dominantLag := 0
		maxCount := -1
		for lag, count := range lagFreqs {
			if count > maxCount {
				maxCount = count
				dominantLag = lag
			}
		}

		// Calculate average correlation across significant windows
		avgCorrelation := 0.0
		if stableAndSigCount > 0 {
			avgCorrelation = sumSignificantCorr / float64(stableAndSigCount)
		}

		// Generate return timestamps
		returnTimestamps := make([]time.Time, len(returnsA))
		for i := 0; i < len(returnsA); i++ {
			returnTimestamps[i] = alignedA[i+1].Timestamp
		}

		expID := fmt.Sprintf("DISC-%s-%s", tickerA, tickerB)
		expRecord := storage.ExperimentRecord{
			ID:           expID,
			Description:  fmt.Sprintf("Discovered Edge: %s vs %s", tickerA, tickerB),
			StrategyType: "LaggedCrossCorrelation",
			TickerA:      tickerA,
			TickerB:      tickerB,
			MacroTicker:  macro,
			WindowSize:   windowSize,
			MaxLag:       maxLag,
			CreatedAt:    time.Now().UTC(),
		}

		_ = db.SaveExperiment(expRecord)
		_ = db.SaveResults(expID, rollingResults, returnTimestamps)
		_ = db.SaveSecondOrderCorrelation(expID, macro, avgCorrelation)

		edge := storage.DiscoveredEdge{
			TickerA:        tickerA,
			TickerB:        tickerB,
			MacroTicker:    macro,
			SurvivalRate:   survivalRate,
			DominantLag:    dominantLag,
			AvgCorrelation: avgCorrelation,
			DiscoveredAt:   time.Now().UTC(),
		}

		// Persist discovered edge
		if err := db.SaveDiscoveredEdge(edge); err != nil {
			return fmt.Errorf("failed to save edge to db: %w", err)
		}

		*discoveredCount++
		fmt.Printf("✦ [EDGE DISCOVERED] %-5s ➔ %-5s | Survival Rate: %5.1f%% | Dominant Lag: %d days | Avg r: %+.4f (Macro: %s)\n",
			tickerA, tickerB, survivalRate*100, dominantLag, avgCorrelation, macro)
	}

	return nil
}
