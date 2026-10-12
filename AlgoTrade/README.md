# AlgoTrade

A research tool for one hypothesis: that one stock's returns follow another's
after a delay, and that the delay shifts with the macro environment.

It downloads price bars, aligns them, and slides a window over the series to
find the lag at which two tickers correlate most strongly, with a volatility
cut-off that marks unstable periods. A scanner tests ticker pairs against a
survival threshold, results go to SQLite or Postgres, and a local dashboard
plots them. [`GUIDE.md`](GUIDE.md) explains the dashboard and its statistics,
and [`ROADMAP.md`](ROADMAP.md) lists what was planned and never built,
including live trading.

It is a Go module of its own and does not use ArtifactFlow:

```bash
go build ./...
go test ./...
```

## Acknowledgement

The idea of building a personal trading research tool was inspired by
[TradingAgents](https://github.com/TauricResearch/TradingAgents), an
open-source Python framework in which LLM agents play the roles of a trading
firm. AlgoTrade takes no code or design from it: it has no agents, and
computes lagged cross-correlation between price series.

Nothing here is investment advice. The code holds no order placement: it
reads prices and reports statistics.
