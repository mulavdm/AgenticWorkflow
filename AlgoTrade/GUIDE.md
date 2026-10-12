# Quantitative Lab - Dashboard & Interpretation Guide

Welcome to your research station. This guide is a living document designed to explain the visual charts, indicators, and statistical metrics displayed on your local web dashboard. As the codebase evolves (e.g., adding vector layers or live order books), this guide will adapt to document new pipeline features.

---

## 📊 1. Core Metrics & Stats Panel

Located at the top of your dashboard, these stats define your active sandbox configuration:

| Metric | Dashboard Display | What it Means (Theoretical Concept) |
| :--- | :--- | :--- |
| **Primary Asset Pairs** | `AAPL ➔ MSFT` | **MSFT is the Leader (Stock B)** and **AAPL is the Follower (Stock A)**. We are testing the hypothesis that MSFT predicts AAPL. |
| **Macro Variable** | `USO` | The Crude Oil ETF. This is our external macroeconomic shock variable used to detect changing structural environments. |
| **Macro Correlation** | `-0.1540` | **Second-Order Correlation.** This is the Pearson correlation between your *Optimal Lag Timeline* and *USO Returns*, indicating how macro shocks modify reaction speed. |
| **Dataset Points** | `380` (approx.) | The number of perfectly aligned historical hours analyzed across our sliding windows. |

---

## 📈 2. The Main Chart: Dynamic Sweet Spot & Volatility Timeline

This chart is the "microscope" of your systematic research pipeline, sliding a window of time forward to track shifting delays.

### The Purple Line: Optimal Lag (Steps)
* **What it is:** The exact hour delay that yielded the highest absolute correlation coefficient in that window.
* **Interpretation:**
  - **First Half:** Sits flat at **Lag 3** (an exact 3-hour delay).
  - **Second Half:** Jumps up and sits flat at **Lag 5** (a 5-hour delay).
  - **Scientific Validation:** This proves your core thesis: **Lead-lag delays are not permanent.** When the macro environment (USO Oil) shifted, the market's reaction speed stretched from 3 to 5 hours!

### The Dotted Gold Line: Rolling Volatility (StdDev)
* **What it is:** The standard deviation of asset returns in the active window.
* **The Regime Circuit Breaker:**
  - Standard correlation is blind to panic. During black swan events, massive price spikes cause standard math to outputs false positives.
  - When the gold line spikes past your threshold, the dots on the purple line turn **Red**, signaling: **" regime unstable — circuit breaker tripped."** This prevents you from executing trades on noisy, non-stationary data.

---

## 📊 3. Profiles & Distributions

### Cross-Correlation Function (CCF) Profile (Bar Chart)
* **What it is:** The overall average correlation computed across the entire historical dataset for each lag.
* **Interpretation:** The tallest bar represents the dominant historical lag. The absolute peak is highlighted in **Green**, serving as your historical baseline.

### Optimal Lag Distribution (Doughnut Chart)
* **What it is:** A count of how often each lag step was the "sweet spot" across the timeline.
* **Interpretation:** You will see two large, distinct slices (Lag 3 and Lag 5). This visually proves that the lead-lag relationship is **bi-modal** (existing in two distinct, healthy economic states) rather than being one noisy, random average.

---

## 🏁 4. Practical Trading Application: How to Trade this Edge

If your dashboard shows the purple line sitting stable at **Lag 5** during high Oil regimes, you have a viable pairs-trading trigger:

1. **Watch the Leader:** You monitor MSFT (the leader) in real-time.
2. **Observe the Jump:** MSFT jumps by `2.0%` at 10:00 AM.
3. **Parameterize the Lag:** Because the current macro regime is high-volatility Oil, you look at your ledger and expect AAPL to follow MSFT's jump in exactly **5 hours**.
4. **Execute the Entry:** You buy AAPL at 10:05 AM, capturing the predictable price gap before the rest of the retail market catches up at 3:00 PM.

---

## 🗺️ 5. Evolution Tracker (Roadmap Sync)

As we build the future milestones documented in `ROADMAP.md`, this guide will expand to cover:

### 🚀 Update A: Vector Semantic States (ChromaDB)
* **What will change:** The dashboard will feature a **"Macro Vibe Search"** card.
* **New Widget:** When a Fed rate decision or news shock drops, the database will query ChromaDB for the top 5 closest historical textual events and plot them as coordinates, showing you the exact lag profiles that succeeded during those past crises.

### 📈 Update B: Live Execution Book (IBKR socket)
* **What will change:** A **"Live Order Book"** terminal.
* **New Widget:** Visualizes active bids, asks, slippage, and OER (Order Efficiency Ratio) throttles, allowing you to watch the Go execution engine buy and sell live.
