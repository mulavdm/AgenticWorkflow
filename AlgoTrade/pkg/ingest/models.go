package ingest

import "time"

// StockBar represents a single price bar for a financial asset (Stock or Future)
type StockBar struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
}

// MacroPoint represents a single data point for a macro economic indicator (e.g. Oil, VIX, Yields)
type MacroPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}
