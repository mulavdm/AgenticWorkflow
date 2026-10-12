package analysis

import (
	"encoding/csv"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// GenerateSyntheticData creates high-fidelity mock price files with standard headers,
// exhibiting a deterministic lead-lag relationship (MSFT leads AAPL by 3 hours in the first half,
// and 5 hours in the second half, correlated with an USO macro shock).
func GenerateSyntheticData(outDir string, n int) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	rand.Seed(12345) // Seed for deterministic mock outputs

	// Timestamps: 1-hour intervals starting from 2026-01-01 09:30:00
	startTime := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)
	timestamps := make([]time.Time, n)
	for i := 0; i < n; i++ {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Hour)
	}

	// 1. Generate MSFT (Leader - Stock B) returns
	msftReturns := make([]float64, n)
	for i := 0; i < n; i++ {
		// Sine wave base + slight random walk noise
		sineBase := math.Sin(float64(i)*0.08) * 0.015
		noise := (rand.Float64() - 0.5) * 0.005
		msftReturns[i] = sineBase + noise
	}

	// 2. Generate AAPL (Follower - Stock A) returns with a dynamic sweet-spot lag
	aaplReturns := make([]float64, n)
	for i := 0; i < n; i++ {
		lag := 3 // First half lag is 3 steps
		if i >= n/2 {
			lag = 5 // Second half lag shifts to 5 steps (the macro regime shift!)
		}

		if i >= lag {
			// Perfect lag correlation plus minor noise
			aaplReturns[i] = msftReturns[i-lag] + (rand.Float64()-0.5)*0.001
		} else {
			aaplReturns[i] = (rand.Float64() - 0.5) * 0.005
		}
	}

	// 3. Generate USO (Macro - Oil ETF) prices
	// USO stays flat/normal during the 3-period lag, then spikes when the lag stretches to 5.
	usoPrices := make([]float64, n)
	for i := 0; i < n; i++ {
		if i < n/2 {
			// Normal range
			usoPrices[i] = 50.0 + math.Cos(float64(i)*0.05)*1.5 + (rand.Float64()-0.5)*0.5
		} else {
			// Spike range representing macro shock
			usoPrices[i] = 95.0 + math.Sin(float64(i)*0.05)*4.0 + (rand.Float64()-0.5)*1.0
		}
	}

	// Calculate Close Prices from returns
	msftPrices := make([]float64, n)
	aaplPrices := make([]float64, n)

	msftPrices[0] = 250.0 // Starting price
	aaplPrices[0] = 180.0 // Starting price

	for i := 1; i < n; i++ {
		msftPrices[i] = msftPrices[i-1] * (1.0 + msftReturns[i])
		aaplPrices[i] = aaplPrices[i-1] * (1.0 + aaplReturns[i])
	}

	// Write MSFT (Leader)
	if err := writeCSV(filepath.Join(outDir, "MSFT.csv"), timestamps, msftPrices); err != nil {
		return fmt.Errorf("failed writing MSFT: %w", err)
	}

	// Write AAPL (Follower)
	if err := writeCSV(filepath.Join(outDir, "AAPL.csv"), timestamps, aaplPrices); err != nil {
		return fmt.Errorf("failed writing AAPL: %w", err)
	}

	// Write USO (Macro)
	if err := writeCSV(filepath.Join(outDir, "USO.csv"), timestamps, usoPrices); err != nil {
		return fmt.Errorf("failed writing USO: %w", err)
	}

	return nil
}

func writeCSV(path string, timestamps []time.Time, prices []float64) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Standard financial headers
	if err := writer.Write([]string{"Timestamp", "Open", "High", "Low", "Close", "Volume"}); err != nil {
		return err
	}

	for i, t := range timestamps {
		pStr := fmt.Sprintf("%.4f", prices[i])
		record := []string{
			t.Format(time.RFC3339),
			pStr, // Open
			pStr, // High
			pStr, // Low
			pStr, // Close
			"100000", // Volume
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	return nil
}
