package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"AlgoTrade/pkg/analysis"
	"AlgoTrade/pkg/dashboard"
	"AlgoTrade/pkg/ingest"
	"AlgoTrade/pkg/storage"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]

	switch command {
	case "generate-mock":
		err := analysis.GenerateSyntheticData("data", 500)
		if err != nil {
			log.Fatalf("Failed to generate synthetic data: %v", err)
		}
		fmt.Println("Successfully generated high-fidelity synthetic market files in 'data/' folder!")

	case "download":
		downloadCmd := flag.NewFlagSet("download", flag.ExitOnError)
		tickerFlag := downloadCmd.String("ticker", "", "Stock ticker (e.g. AAPL, MSFT, SPY)")
		startFlag := downloadCmd.String("start", "", "Start date YYYY-MM-DD")
		endFlag := downloadCmd.String("end", "", "End date YYYY-MM-DD")

		downloadCmd.Parse(os.Args[2:])

		if *tickerFlag == "" || *startFlag == "" || *endFlag == "" {
			log.Fatalf("Error: --ticker, --start, and --end flags are all required")
		}

		tStr, err := time.Parse("2006-01-02", *startFlag)
		if err != nil {
			log.Fatalf("Invalid start date format (use YYYY-MM-DD): %v", err)
		}
		tEnd, err := time.Parse("2006-01-02", *endFlag)
		if err != nil {
			log.Fatalf("Invalid end date format (use YYYY-MM-DD): %v", err)
		}

		err = ingest.FetchYahooCSV(*tickerFlag, tStr, tEnd, "data")
		if err != nil {
			log.Fatalf("Download failed: %v", err)
		}

	case "run":
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		configFlag := runCmd.String("config", "config.json", "Path to the strategy manifest JSON")
		dbTypeFlag := runCmd.String("db", "sqlite", "Database type: 'sqlite' or 'postgres'")
		connStrFlag := runCmd.String("conn", "", "PostgreSQL connection string")

		runCmd.Parse(os.Args[2:])

		db, err := initDB(*dbTypeFlag, *connStrFlag)
		if err != nil {
			log.Fatalf("Database initialization failed: %v", err)
		}
		defer db.Close()

		if err := db.InitializeSchema(); err != nil {
			log.Fatalf("Failed to run DDL schema migration: %v", err)
		}

		runner := analysis.NewRunner("data")
		ctx := context.Background()
		if err := runner.Run(ctx, db, *configFlag); err != nil {
			log.Fatalf("Experiment execution failed: %v", err)
		}

	case "serve":
		serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
		dbTypeFlag := serveCmd.String("db", "sqlite", "Database type: 'sqlite' or 'postgres'")
		connStrFlag := serveCmd.String("conn", "", "PostgreSQL connection string")
		portFlag := serveCmd.Int("port", 8080, "Port to run web server on")

		serveCmd.Parse(os.Args[2:])

		db, err := initDB(*dbTypeFlag, *connStrFlag)
		if err != nil {
			log.Fatalf("Database initialization failed: %v", err)
		}
		defer db.Close()

		if err := db.InitializeSchema(); err != nil {
			log.Fatalf("Failed to run DDL schema migration: %v", err)
		}

		server := dashboard.NewDashboardServer(db, "data", *portFlag)
		if err := server.Start(); err != nil {
			log.Fatalf("Local dashboard server crashed: %v", err)
		}

	case "scan":
		scanCmd := flag.NewFlagSet("scan", flag.ExitOnError)
		dbTypeFlag := scanCmd.String("db", "sqlite", "Database type: 'sqlite' or 'postgres'")
		connStrFlag := scanCmd.String("conn", "", "PostgreSQL connection string")
		thresholdFlag := scanCmd.Float64("threshold", 0.20, "Survival threshold for non-falsified edges (0.0 to 1.0)")

		scanCmd.Parse(os.Args[2:])

		db, err := initDB(*dbTypeFlag, *connStrFlag)
		if err != nil {
			log.Fatalf("Database initialization failed: %v", err)
		}
		defer db.Close()

		if err := db.InitializeSchema(); err != nil {
			log.Fatalf("Failed to run DDL schema migration: %v", err)
		}

		scanner := analysis.NewPopperianScanner("data")
		ctx := context.Background()
		if err := scanner.Scan(ctx, db, *thresholdFlag); err != nil {
			log.Fatalf("Universe scanning failed: %v", err)
		}

	default:
		printUsage()
	}
}

func initDB(dbType, connStr string) (storage.Storage, error) {
	if dbType == "postgres" {
		if connStr == "" {
			connStr = "postgres://postgres:postgres@localhost:5432/algo_trade?sslmode=disable"
		}
		return storage.NewPostgresStorage(connStr)
	}

	// Default to SQLite
	return storage.NewSQLiteStorage("experiments.db")
}

func printUsage() {
	fmt.Println("Usage: main.go <command> [arguments]")
	fmt.Println("Commands:")
	fmt.Println("  generate-mock    Generate synthetic market CSVs inside 'data/' folder")
	fmt.Println("  download         Download historical CSV data from Yahoo Finance")
	fmt.Println("                   Flags: --ticker <ticker> (required)")
	fmt.Println("                          --start <YYYY-MM-DD> (required)")
	fmt.Println("                          --end <YYYY-MM-DD> (required)")
	fmt.Println("  run              Execute a research experiment manifest")
	fmt.Println("                   Flags: --config <path> (default: config.json)")
	fmt.Println("                          --db [sqlite|postgres] (default: sqlite)")
	fmt.Println("                          --conn <postgres_conn_str>")
	fmt.Println("  scan             Auto-scan the data universe for non-falsified edges")
	fmt.Println("                   Flags: --db [sqlite|postgres] (default: sqlite)")
	fmt.Println("                          --conn <postgres_conn_str>")
	fmt.Println("                          --threshold <float> (default: 0.20)")
	fmt.Println("  serve            Launch local research dashboard web interface")
	fmt.Println("                   Flags: --db [sqlite|postgres] (default: sqlite)")
	fmt.Println("                          --conn <postgres_conn_str>")
	fmt.Println("                          --port <port> (default: 8080)")
}
