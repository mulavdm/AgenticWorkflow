# Quantitative Research Lab Roadmap

This roadmap documents our future engineering goals and integrations to scale this systematic research engine into a production-grade algorithmic trading workstation.

---

## 🚀 Phase 1: Semantic Vector Search Layer (ChromaDB Integration)

To move past basic statistical time-series and ingest unstructured economic data (news headlines, Federal Reserve meeting transcripts, earnings calls):

1. **Local Vector Database Deployment:**
   - Deploy **ChromaDB** as a Dockerized microservice inside our `docker-compose.yml`.
2. **Text Embedding Pipeline:**
   - Integrate with a local **Ollama** instance running a modern embedding model (e.g., `nomic-embed-text` or `all-minilm`).
   - Write a Go connector wrapper package (`pkg/vectorstore`) via `github.com/amikos-tech/chroma-go` to embed and index macro articles.
3. **Macro Context Querying:**
   - Map historical date IDs between the vector store (ChromaDB) and our relational database (SQLite/PostgreSQL).
   - Implement semantic search matching today's market context with the top $K$ closest historical regimes, automatically fetching their successful statistical lags to parameterize active models dynamically.

---

## 📈 Phase 2: Live Market Integration (IBKR Socket Integration)

Transition from offline backtesting on static CSVs to real-time live tick capture and automated order submission:

1. **TWS / IB Gateway Client:**
   - Integrate a native Go IBKR client (e.g., `github.com/zoid007/ibapi`) to communicate with a locally running Trader Workstation or IB Gateway via local TCP socket ports.
2. **Websocket Streaming Handler:**
   - Build a real-time event dispatcher in Go to stream live bid/ask quotes and transaction ticks into our memory ring buffers.
3. **Dynamic Order Dispatcher:**
   - Implement an automated limit order executor that places trade entries and exits the millisecond a high-probability lagged gap convergence signal is triggered.

---

## 🛡️ Phase 3: Defensive Risk Safeguards & Execution Controls

Build an institutional-grade defense system to protect trading capital from structural failures, extreme volatility, and broker penalties:

1. **Order Efficiency Ratio (OER) Throttler:**
   - Implement an automated sliding-window rate-limiter that throttles order modifications/cancellations to ensure compliance with Interactive Brokers' OER rules and avoid message penalties.
2. **Real-Time Margin Buffer:**
   - Write a real-time account auditor that monitors free margin, open position sizes, and broker maintenance limits.
   - Programmatically block execution or scale down size if account equity approaches liquidation thresholds, maintaining an active buffer against IBKR's automated liquidation engine.
3. **Regime Breakout Circuit Breakers:**
   - Add rolling Z-Score volatility monitors to detect sudden news shocks. 
   - Temporarily pause core strategy executions and cancel open orders during central bank announcements (Federal Reserve rate releases, NFP reporting) until volatility clusters clear.
