package ingest

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseTime attempts to parse date/timestamp strings with common financial formats
func ParseTime(str string) (time.Time, error) {
	str = strings.TrimSpace(str)
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, str); err == nil {
			return t, nil
		}
	}
	// Try parsing as UNIX timestamp string
	if secs, err := strconv.ParseInt(str, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC(), nil
	}

	return time.Time{}, fmt.Errorf("unable to parse time string: %s", str)
}

// LoadCSV reads a standard financial CSV and returns a slice of StockBars.
// It uses case-insensitive header mapping and falls back to index order.
func LoadCSV(filePath string) ([]StockBar, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV contents: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV file contains insufficient rows (minimum 2 needed)")
	}

	// Dynamic Header Mapping
	headers := records[0]
	idxTime := -1
	idxOpen := -1
	idxHigh := -1
	idxLow := -1
	idxClose := -1
	idxVolume := -1

	for i, h := range headers {
		hLower := strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.Contains(hLower, "date") || strings.Contains(hLower, "timestamp") || strings.Contains(hLower, "time"):
			idxTime = i
		case strings.Contains(hLower, "open"):
			idxOpen = i
		case strings.Contains(hLower, "high"):
			idxHigh = i
		case strings.Contains(hLower, "low"):
			idxLow = i
		case strings.Contains(hLower, "close"):
			idxClose = i
		case strings.Contains(hLower, "volume") || strings.Contains(hLower, "vol"):
			idxVolume = i
		}
	}

	// Fallback to default indices if headers are not detected or duplicate/empty
	if idxTime == -1 {
		idxTime = 0
	}
	if idxOpen == -1 {
		idxOpen = 1
	}
	if idxHigh == -1 {
		idxHigh = 2
	}
	if idxLow == -1 {
		idxLow = 3
	}
	if idxClose == -1 {
		idxClose = 4
	}
	if idxVolume == -1 {
		idxVolume = 5
	}

	var data []StockBar
	for rowIdx := 1; rowIdx < len(records); rowIdx++ {
		row := records[rowIdx]
		if len(row) <= idxTime || len(row) <= idxClose {
			continue // Skip malformed rows
		}

		t, err := ParseTime(row[idxTime])
		if err != nil {
			continue // Skip rows with unparseable timestamps
		}

		closeVal, err := strconv.ParseFloat(row[idxClose], 64)
		if err != nil {
			continue // Skip rows with invalid close price
		}

		openVal := closeVal
		if idxOpen < len(row) {
			if v, err := strconv.ParseFloat(row[idxOpen], 64); err == nil {
				openVal = v
			}
		}

		highVal := closeVal
		if idxHigh < len(row) {
			if v, err := strconv.ParseFloat(row[idxHigh], 64); err == nil {
				highVal = v
			}
		}

		lowVal := closeVal
		if idxLow < len(row) {
			if v, err := strconv.ParseFloat(row[idxLow], 64); err == nil {
				lowVal = v
			}
		}

		volumeVal := 0.0
		if idxVolume < len(row) {
			if v, err := strconv.ParseFloat(row[idxVolume], 64); err == nil {
				volumeVal = v
			}
		}

		data = append(data, StockBar{
			Timestamp: t.UTC(),
			Open:      openVal,
			High:      highVal,
			Low:       lowVal,
			Close:     closeVal,
			Volume:    volumeVal,
		})
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("no valid stock data bars parsed from CSV")
	}

	return data, nil
}

// CalculateReturns converts raw close prices in StockBars to standard percentage changes.
// The output slice size is len(bars) - 1. 
// Output returns[i] corresponds to the change from bars[i] to bars[i+1], mapped to bars[i+1]'s timestamp.
func CalculateReturns(bars []StockBar) []float64 {
	if len(bars) < 2 {
		return nil
	}

	returns := make([]float64, len(bars)-1)
	for i := 1; i < len(bars); i++ {
		prev := bars[i-1].Close
		curr := bars[i].Close
		if prev == 0 {
			returns[i-1] = 0.0
		} else {
			returns[i-1] = (curr - prev) / prev
		}
	}
	return returns
}
