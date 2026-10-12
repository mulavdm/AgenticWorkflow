package math

import (
	"errors"
	"math"
)

// SweetSpotResult represents the optimal correlation and lag discovered for a time-series window,
// along with rigorous hypothesis testing metrics to falsify spurious patterns.
type SweetSpotResult struct {
	WindowEndIndex int     `json:"window_end_index"`
	OptimalLag     int     `json:"optimal_lag"`
	Correlation    float64 `json:"correlation"`
	Volatility     float64 `json:"volatility"`
	IsStable       bool    `json:"is_stable"`
	TStatistic     float64 `json:"t_statistic"`
	PValue         float64 `json:"p_value"`
	IsSignificant  bool    `json:"is_significant"`
}

// Mean calculates the arithmetic mean of a slice
func Mean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

// Variance calculates the variance of a slice given its mean
func Variance(data []float64, mean float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sumSqDiff := 0.0
	for _, v := range data {
		diff := v - mean
		sumSqDiff += diff * diff
	}
	return sumSqDiff / float64(len(data))
}

// StdDev calculates the standard deviation of a slice given its mean
func StdDev(data []float64, mean float64) float64 {
	return math.Sqrt(Variance(data, mean))
}

// LaggedCorrelation calculates the Pearson correlation coefficient between a and b,
// where b is delayed by 'lag' periods.
// Formally maps: a[lag:] (Today) against b[:len(b)-lag] (Yesterday)
func LaggedCorrelation(a, b []float64, lag int) (float64, error) {
	if len(a) != len(b) {
		return 0, errors.New("time series slices must be of equal length")
	}
	if lag < 0 {
		return 0, errors.New("lag must be non-negative")
	}
	if lag >= len(a) {
		return 0, errors.New("lag cannot be greater than or equal to dataset length")
	}

	sliceA := a[lag:]
	sliceB := b[:len(b)-lag]
	n := len(sliceA)

	if n == 0 {
		return 0, nil
	}

	meanA := Mean(sliceA)
	meanB := Mean(sliceB)

	var covariance, varianceA, varianceB float64
	for i := 0; i < n; i++ {
		diffA := sliceA[i] - meanA
		diffB := sliceB[i] - meanB

		covariance += diffA * diffB
		varianceA += diffA * diffA
		varianceB += diffB * diffB
	}

	if varianceA == 0 || varianceB == 0 {
		return 0, nil // Avoid division by zero if prices didn't move
	}

	return covariance / math.Sqrt(varianceA*varianceB), nil
}

// RollingSweetSpot slides a moving window across a and b, computing the optimal lagged
// correlation for each window and checking if volatility triggers the stability circuit breaker.
func RollingSweetSpot(a, b []float64, windowSize, stepSize, maxLag int, volThreshold float64) []SweetSpotResult {
	if len(a) != len(b) || len(a) < windowSize || windowSize <= maxLag {
		return nil
	}

	var results []SweetSpotResult

	for start := 0; start <= len(a)-windowSize; start += stepSize {
		end := start + windowSize
		subA := a[start:end]
		subB := b[start:end]

		// Calculate volatility for Stock A (standard deviation)
		meanA := Mean(subA)
		volA := StdDev(subA, meanA)

		// Volatility circuit breaker check
		isStable := true
		if volThreshold > 0 && volA > volThreshold {
			isStable = false
		}

		bestLag := 0
		maxAbsCorr := -1.0
		bestCorr := 0.0

		// Find optimal lag in this window
		for lag := 0; lag <= maxLag; lag++ {
			corr, err := LaggedCorrelation(subA, subB, lag)
			if err != nil {
				continue
			}

			absCorr := math.Abs(corr)
			if !math.IsNaN(corr) && absCorr > maxAbsCorr {
				maxAbsCorr = absCorr
				bestCorr = corr
				bestLag = lag
			}
		}

		// Calculate statistical significance for Pearson r
		// Sample size for lagged correlation is windowSize - bestLag
		nSub := windowSize - bestLag
		tStat, pVal := PearsonSignificance(bestCorr, nSub)

		// Apply Bonferroni correction to adjust for multiple testing (family-wise error rate)
		alpha := 0.05
		numTests := maxLag + 1
		alphaAdj := alpha / float64(numTests)
		isSignificant := pVal < alphaAdj

		results = append(results, SweetSpotResult{
			WindowEndIndex: end,
			OptimalLag:     bestLag,
			Correlation:    bestCorr,
			Volatility:     volA,
			IsStable:       isStable,
			TStatistic:     tStat,
			PValue:         pVal,
			IsSignificant:  isSignificant,
		})
	}

	return results
}

// StudentTTwoTailedPValue calculates the two-tailed p-value for a given t-statistic
// and degrees of freedom (df) using a highly accurate, self-contained normal approximation with correction.
func StudentTTwoTailedPValue(t float64, df float64) float64 {
	if df <= 0 {
		return 1.0
	}
	absT := math.Abs(t)
	// Standard normal approximation with degrees-of-freedom correction
	z := absT * (1.0 - 1.0/(4.0*df)) / math.Sqrt(1.0 + (absT*absT)/(2.0*df))
	return math.Erfc(z / math.Sqrt(2.0))
}

// PearsonSignificance computes the t-statistic and two-tailed p-value for a Pearson r
// given a sample size of n points.
func PearsonSignificance(r float64, n int) (tStat float64, pValue float64) {
	if n <= 2 {
		return 0.0, 1.0
	}
	if math.Abs(r) >= 1.0 {
		return math.Inf(1), 0.0
	}
	df := float64(n - 2)
	tStat = r * math.Sqrt(df / (1.0 - r*r))
	pValue = StudentTTwoTailedPValue(tStat, df)
	return tStat, pValue
}
