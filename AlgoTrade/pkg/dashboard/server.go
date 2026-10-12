package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	stdmath "math"
	"net/http"
	"path/filepath"

	"AlgoTrade/pkg/ingest"
	"AlgoTrade/pkg/math"
	"AlgoTrade/pkg/storage"
)

//go:embed web/*
var webAssets embed.FS

type DashboardServer struct {
	db      storage.Storage
	dataDir string
	port    int
}

func NewDashboardServer(db storage.Storage, dataDir string, port int) *DashboardServer {
	return &DashboardServer{
		db:      db,
		dataDir: dataDir,
		port:    port,
	}
}

func (s *DashboardServer) Start() error {
	// Serve static files from the embedded FS
	subFS, err := fs.Sub(webAssets, "web")
	if err != nil {
		return fmt.Errorf("failed to locate embedded web directory: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(subFS)))

	// API Endpoints
	mux.HandleFunc("/api/experiments", s.handleGetExperiments)
	mux.HandleFunc("/api/results", s.handleGetExperimentResults)
	mux.HandleFunc("/api/edges", s.handleGetDiscoveredEdges)

	fmt.Printf("[Dashboard] Local Server running on http://localhost:%d\n", s.port)
	return http.ListenAndServe(fmt.Sprintf(":%d", s.port), mux)
}

func (s *DashboardServer) handleGetDiscoveredEdges(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	edges, err := s.db.GetDiscoveredEdges()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed querying discovered edges: %v", err), http.StatusInternalServerError)
		return
	}
	if edges == nil {
		edges = []storage.DiscoveredEdge{}
	}
	json.NewEncoder(w).Encode(edges)
}

func (s *DashboardServer) handleGetExperiments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	records, err := s.db.GetExperiments()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed querying experiments: %v", err), http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []storage.ExperimentRecord{}
	}
	json.NewEncoder(w).Encode(records)
}

func (s *DashboardServer) handleGetExperimentResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "Missing experiment 'id' query parameter", http.StatusBadRequest)
		return
	}

	// 1. Fetch historical results from DB
	results, err := s.db.GetExperimentResults(id)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed querying results: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Fetch second order correlation from DB
	secondOrderCorr, err := s.db.GetSecondOrderCorrelation(id)
	if err != nil {
		secondOrderCorr = 0.0
	}

	// 3. Compute dynamic Overall CCF Profile (average across whole history) to feed the UI bar chart
	fullCCF := []struct {
		Lag         int     `json:"lag"`
		Correlation float64 `json:"correlation"`
	}{}

	// Get experiment record to find tickers & parameters
	experiments, err := s.db.GetExperiments()
	if err == nil {
		var activeExp storage.ExperimentRecord
		found := false
		for _, exp := range experiments {
			if exp.ID == id {
				activeExp = exp
				found = true
				break
			}
		}

		if found {
			aPath := filepath.Join(s.dataDir, activeExp.TickerA+".csv")
			bPath := filepath.Join(s.dataDir, activeExp.TickerB+".csv")
			barsA, errA := ingest.LoadCSV(aPath)
			barsB, errB := ingest.LoadCSV(bPath)

			if errA == nil && errB == nil {
				alignedA, alignedB := ingest.AlignStockBars(barsA, barsB)
				returnsA := ingest.CalculateReturns(alignedA)
				returnsB := ingest.CalculateReturns(alignedB)

				for lag := 0; lag <= activeExp.MaxLag; lag++ {
					corr, err := math.LaggedCorrelation(returnsA, returnsB, lag)
					if err == nil && !stdmath.IsNaN(corr) {
						fullCCF = append(fullCCF, struct {
							Lag         int     `json:"lag"`
							Correlation float64 `json:"correlation"`
						}{Lag: lag, Correlation: corr})
					} else {
						fullCCF = append(fullCCF, struct {
							Lag         int     `json:"lag"`
							Correlation float64 `json:"correlation"`
						}{Lag: lag, Correlation: 0.0})
					}
				}
			}
		}
	}

	response := struct {
		Results                 []storage.ResultRecord `json:"results"`
		SecondOrderCorrelation float64                `json:"second_order_correlation"`
		FullCCF                 []struct {
			Lag         int     `json:"lag"`
			Correlation float64 `json:"correlation"`
		} `json:"full_ccf"`
	}{
		Results:                 results,
		SecondOrderCorrelation: secondOrderCorr,
		FullCCF:                 fullCCF,
	}

	json.NewEncoder(w).Encode(response)
}
