package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type YahooChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error interface{} `json:"error"`
	} `json:"chart"`
}

// FetchYahooCSV downloads historical daily price data from Yahoo Finance's public v8 chart API
// (which does not require cookies/crumbs), parses the JSON, and saves it as a standard CSV in data/.
func FetchYahooCSV(ticker string, start, end time.Time, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	period1 := start.Unix()
	period2 := end.Unix()

	// v8 chart query has no 401 cookie restrictions
	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=1d",
		ticker, period1, period2,
	)

	fmt.Printf("[Downloader] Requesting Yahoo Chart JSON API: %s\n", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("yahoo finance chart API returned non-OK status: %d, response: %s", resp.StatusCode, string(bodyBytes))
	}

	var chartResp YahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&chartResp); err != nil {
		return fmt.Errorf("failed parsing Yahoo chart response JSON: %w", err)
	}

	if len(chartResp.Chart.Result) == 0 {
		return fmt.Errorf("yahoo chart response contains empty result results")
	}

	result := chartResp.Chart.Result[0]
	timestamps := result.Timestamp
	if len(timestamps) == 0 {
		return fmt.Errorf("no price data timestamps returned for %s", ticker)
	}

	quote := result.Indicators.Quote[0]

	outPath := filepath.Join(outDir, fmt.Sprintf("%s.csv", ticker))
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("failed creating output file %s: %w", outPath, err)
	}
	defer outFile.Close()

	// Write standard financial CSV headers
	_, err = fmt.Fprintln(outFile, "Timestamp,Open,High,Low,Close,Volume")
	if err != nil {
		return fmt.Errorf("failed writing headers: %w", err)
	}

	for i, ts := range timestamps {
		// Yahoo arrays might have null elements (represented as nil pointers in Go struct)
		var openVal, highVal, lowVal, closeVal float64
		var volVal int64

		if i < len(quote.Open) && quote.Open[i] != nil {
			openVal = *quote.Open[i]
		}
		if i < len(quote.High) && quote.High[i] != nil {
			highVal = *quote.High[i]
		}
		if i < len(quote.Low) && quote.Low[i] != nil {
			lowVal = *quote.Low[i]
		}
		if i < len(quote.Close) && quote.Close[i] != nil {
			closeVal = *quote.Close[i]
		}
		if i < len(quote.Volume) && quote.Volume[i] != nil {
			volVal = *quote.Volume[i]
		}

		t := time.Unix(ts, 0).UTC()
		row := fmt.Sprintf("%s,%.4f,%.4f,%.4f,%.4f,%d",
			t.Format(time.RFC3339),
			openVal,
			highVal,
			lowVal,
			closeVal,
			volVal,
		)
		_, err = fmt.Fprintln(outFile, row)
		if err != nil {
			return fmt.Errorf("failed writing row data to file: %w", err)
		}
	}

	fmt.Printf("[Downloader] Successfully parsed and saved daily Yahoo data for %s to %s (%d records)\n", ticker, outPath, len(timestamps))
	return nil
}
