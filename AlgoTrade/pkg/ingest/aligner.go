package ingest

import (
	"sort"
	"time"
)

// Helper functions to check if data is daily resolution
func isDailyBars(bars []StockBar) bool {
	if len(bars) < 2 {
		return true
	}
	var totalDiff int64
	count := 0
	for i := 1; i < len(bars) && i < 15; i++ {
		diff := bars[i].Timestamp.Unix() - bars[i-1].Timestamp.Unix()
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
		count++
	}
	if count == 0 {
		return true
	}
	avgSecs := totalDiff / int64(count)
	return avgSecs >= 12*3600 // 12 hours
}

func isDailyMacro(points []MacroPoint) bool {
	if len(points) < 2 {
		return true
	}
	var totalDiff int64
	count := 0
	for i := 1; i < len(points) && i < 15; i++ {
		diff := points[i].Timestamp.Unix() - points[i-1].Timestamp.Unix()
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
		count++
	}
	if count == 0 {
		return true
	}
	avgSecs := totalDiff / int64(count)
	return avgSecs >= 12*3600 // 12 hours
}

// AlignStockBars aligns two slices of StockBars by their timestamp.
// If both slices represent daily data, it normalizes timestamps to midnight UTC
// to ignore timezone/trading-hour variations, otherwise it does an exact match.
// It returns two aligned slices of equal length, sorted chronologically.
func AlignStockBars(a, b []StockBar) ([]StockBar, []StockBar) {
	dailyA := isDailyBars(a)
	dailyB := isDailyBars(b)
	useMidnight := dailyA && dailyB

	mapA := make(map[int64]StockBar)
	for _, bar := range a {
		t := bar.Timestamp
		if useMidnight {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		bar.Timestamp = t
		mapA[t.Unix()] = bar
	}

	mapB := make(map[int64]StockBar)
	for _, bar := range b {
		t := bar.Timestamp
		if useMidnight {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		bar.Timestamp = t
		mapB[t.Unix()] = bar
	}

	// Find intersection of Unix seconds
	var sharedTimestamps []int64
	for ts := range mapA {
		if _, ok := mapB[ts]; ok {
			sharedTimestamps = append(sharedTimestamps, ts)
		}
	}

	// Sort chronologically
	sort.Slice(sharedTimestamps, func(i, j int) bool {
		return sharedTimestamps[i] < sharedTimestamps[j]
	})

	alignedA := make([]StockBar, len(sharedTimestamps))
	alignedB := make([]StockBar, len(sharedTimestamps))

	for i, ts := range sharedTimestamps {
		alignedA[i] = mapA[ts]
		alignedB[i] = mapB[ts]
	}

	return alignedA, alignedB
}

// AlignWithMacro aligns a slice of StockBars (typically representing asset data or results)
// with a slice of MacroPoints. If both represent daily resolution, they are matched 
// at midnight UTC dates, otherwise by exact timestamp.
// Returns two aligned slices sorted chronologically.
func AlignWithMacro(bars []StockBar, macro []MacroPoint) ([]StockBar, []MacroPoint) {
	dailyBars := isDailyBars(bars)
	dailyMacro := isDailyMacro(macro)
	useMidnight := dailyBars && dailyMacro

	mapBars := make(map[int64]StockBar)
	for _, bar := range bars {
		t := bar.Timestamp
		if useMidnight {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		bar.Timestamp = t
		mapBars[t.Unix()] = bar
	}

	mapMacro := make(map[int64]MacroPoint)
	for _, m := range macro {
		t := m.Timestamp
		if useMidnight {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		m.Timestamp = t
		mapMacro[t.Unix()] = m
	}

	var sharedTimestamps []int64
	for ts := range mapBars {
		if _, ok := mapMacro[ts]; ok {
			sharedTimestamps = append(sharedTimestamps, ts)
		}
	}

	sort.Slice(sharedTimestamps, func(i, j int) bool {
		return sharedTimestamps[i] < sharedTimestamps[j]
	})

	alignedBars := make([]StockBar, len(sharedTimestamps))
	alignedMacro := make([]MacroPoint, len(sharedTimestamps))

	for i, ts := range sharedTimestamps {
		alignedBars[i] = mapBars[ts]
		alignedMacro[i] = mapMacro[ts]
	}

	return alignedBars, alignedMacro
}
