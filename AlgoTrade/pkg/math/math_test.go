package math

import (
	"math"
	"testing"
)

func TestMean(t *testing.T) {
	data := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	expected := 3.0
	actual := Mean(data)
	if math.Abs(actual-expected) > 1e-9 {
		t.Errorf("Mean failed: expected %f, got %f", expected, actual)
	}
}

func TestStdDev(t *testing.T) {
	data := []float64{2.0, 4.0, 4.0, 4.0, 5.0, 5.0, 7.0, 9.0}
	mean := Mean(data) // 5.0
	expected := 2.0    // Population standard deviation (variance = 4.0)
	actual := StdDev(data, mean)
	if math.Abs(actual-expected) > 1e-9 {
		t.Errorf("StdDev failed: expected %f, got %f", expected, actual)
	}
}

func TestLaggedCorrelation(t *testing.T) {
	// Stock B percentage returns (leads Stock A)
	b := []float64{0.01, -0.02, 0.03, 0.015, -0.01, 0.025, -0.005}
	// Stock A is an exact 2-step delayed copy of Stock B (padded with zeros at start)
	a := []float64{0.00, 0.00, 0.01, -0.02, 0.03, 0.015, -0.01}

	// Test Lag 2 (where correlation should be 1.0)
	corrLag2, err := LaggedCorrelation(a, b, 2)
	if err != nil {
		t.Fatalf("LaggedCorrelation returned unexpected error at lag 2: %v", err)
	}
	if math.Abs(corrLag2-1.0) > 1e-9 {
		t.Errorf("LaggedCorrelation failed at lag 2: expected 1.0000, got %f", corrLag2)
	}

	// Test Lag 0 (should be low/negative or at least not 1.0)
	corrLag0, err := LaggedCorrelation(a, b, 0)
	if err != nil {
		t.Fatalf("LaggedCorrelation returned unexpected error at lag 0: %v", err)
	}
	if math.Abs(corrLag0-1.0) < 0.1 {
		t.Errorf("LaggedCorrelation failed at lag 0: expected non-perfect correlation, got %f", corrLag0)
	}
}

func TestRollingSweetSpot(t *testing.T) {
	// Generate longer mock series
	// b: random walk/sine style wave (leads Stock A)
	// a: lagged by exactly 3 steps
	n := 100
	a := make([]float64, n)
	b := make([]float64, n)

	for i := 0; i < n; i++ {
		val := math.Sin(float64(i) * 0.1)
		b[i] = val
		if i >= 3 {
			a[i] = b[i-3]
		} else {
			a[i] = 0.0
		}
	}

	windowSize := 30
	stepSize := 5
	maxLag := 5
	volThreshold := 1.5 // high enough not to trigger stable=false

	results := RollingSweetSpot(a, b, windowSize, stepSize, maxLag, volThreshold)

	if len(results) == 0 {
		t.Fatal("RollingSweetSpot returned empty results")
	}

	// For windows that don't include the very start index (where padding was used),
	// the optimal lag should be exactly 3.
	for _, res := range results {
		if res.WindowEndIndex > 15 {
			if res.OptimalLag != 3 {
				t.Errorf("RollingSweetSpot failed: expected optimal lag 3 at end index %d, got %d", res.WindowEndIndex, res.OptimalLag)
			}
			if !res.IsStable {
				t.Errorf("RollingSweetSpot failed: expected stable=true, got stable=false")
			}
		}
	}
}
