document.addEventListener('DOMContentLoaded', () => {
    let timelineChart = null;
    let ccfChart = null;
    let distributionChart = null;

    // Fetch and populate experiment list
    fetch('/api/experiments')
        .then(res => res.json())
        .then(experiments => {
            const listEl = document.getElementById('experiment-list');
            listEl.innerHTML = '';

            if (!experiments || experiments.length === 0) {
                listEl.innerHTML = `<li class="no-data-msg">No runs logged yet</li>`;
                return;
            }

            experiments.forEach((exp, idx) => {
                const li = document.createElement('li');
                li.className = `experiment-item ${idx === 0 ? 'active' : ''}`;
                li.innerHTML = `
                    <div class="exp-id">${exp.id}</div>
                    <div class="exp-desc">${exp.description}</div>
                `;
                li.addEventListener('click', () => {
                    document.querySelectorAll('.experiment-item').forEach(el => el.classList.remove('active'));
                    li.classList.add('active');
                    loadExperiment(exp);
                });
                listEl.appendChild(li);
            });

            // Automatically load first experiment
            loadExperiment(experiments[0]);

            // Fetch discovered edges for Popperian Discovery Lab
            fetch('/api/edges')
                .then(res => res.json())
                .then(edges => {
                    const tableEl = document.getElementById('edges-table-body');
                    const cardEl = document.getElementById('discovery-lab-card');
                    if (!tableEl || !cardEl) return;
                    tableEl.innerHTML = '';

                    if (!edges || edges.length === 0) {
                        cardEl.style.display = 'none';
                        return;
                    }

                    cardEl.style.display = 'block';
                    edges.forEach(edge => {
                        const tr = document.createElement('tr');
                        
                        // Survival rate badge styling
                        let badgeClass = '';
                        if (edge.survival_rate >= 0.50) {
                            badgeClass = 'high';
                        } else if (edge.survival_rate >= 0.30) {
                            badgeClass = 'mid';
                        }
                        
                        const dateStr = new Date(edge.discovered_at).toLocaleDateString();
                        
                        tr.innerHTML = `
                            <td style="padding: 0.6rem 0.75rem; font-weight: 500;">${edge.ticker_a}</td>
                            <td style="padding: 0.6rem 0.75rem; font-weight: 500; color: var(--primary);">${edge.ticker_b}</td>
                            <td style="padding: 0.6rem 0.75rem; color: var(--text-muted);">${edge.macro_ticker}</td>
                            <td style="padding: 0.6rem 0.75rem; text-align: center;">${edge.dominant_lag} days</td>
                            <td style="padding: 0.6rem 0.75rem; text-align: center; font-weight: 600;">${edge.avg_correlation.toFixed(4)}</td>
                            <td style="padding: 0.6rem 0.75rem; text-align: center;">
                                <span class="survival-badge ${badgeClass}">${(edge.survival_rate * 100).toFixed(1)}%</span>
                            </td>
                            <td style="padding: 0.6rem 0.75rem; text-align: right; color: var(--text-muted); font-size: 0.8rem;">${dateStr}</td>
                        `;
                        
                        // Click row to dynamically load results chart!
                        tr.addEventListener('click', () => {
                            const discExpId = `DISC-${edge.ticker_a}-${edge.ticker_b}`;
                            const foundExp = experiments.find(e => e.id === discExpId);
                            if (foundExp) {
                                loadExperiment(foundExp);
                                document.querySelectorAll('.experiment-item').forEach(el => {
                                    const idEl = el.querySelector('.exp-id');
                                    if (idEl && idEl.textContent === discExpId) {
                                        el.classList.add('active');
                                    } else {
                                        el.classList.remove('active');
                                    }
                                });
                            } else {
                                const fallbackExp = {
                                    id: discExpId,
                                    description: `Discovered Edge: ${edge.ticker_a} vs ${edge.ticker_b}`,
                                    strategy_type: 'LaggedCrossCorrelation',
                                    ticker_a: edge.ticker_a,
                                    ticker_b: edge.ticker_b,
                                    macro_ticker: edge.macro_ticker,
                                    window_size: 60,
                                    max_lag: 10,
                                    created_at: edge.discovered_at
                                };
                                loadExperiment(fallbackExp);
                            }
                        });
                        
                        tableEl.appendChild(tr);
                    });
                })
                .catch(err => console.error('Error fetching edges:', err));
        })
        .catch(err => {
            console.error('Error fetching experiments:', err);
            document.getElementById('experiment-list').innerHTML = `<li class="no-data-msg" style="color: var(--alert-red);">Failed to connect to local server</li>`;
        });

    function loadExperiment(exp) {
        if (!exp) return;

        // Update meta card
        document.getElementById('active-exp-title').textContent = `${exp.description}`;
        document.getElementById('active-exp-desc').textContent = `Strategy Type: ${exp.strategy_type} | Lookback Window: ${exp.window_size} bars | Max Lag Tested: ${exp.max_lag} steps. Run Date: ${new Date(exp.created_at).toLocaleString()}`;

        // Populate basic stats placeholders
        document.getElementById('stat-pairs').textContent = `${exp.ticker_a} ➔ ${exp.ticker_b}`;
        document.getElementById('stat-macro').textContent = exp.macro_ticker;

        // Fetch detailed results for this experiment
        fetch(`/api/results?id=${exp.id}`)
            .then(res => res.json())
            .then(data => {
                const results = data.results || [];
                const fullCCF = data.full_ccf || [];
                const secondOrderCorr = data.second_order_correlation;

                // Update second-order stats
                document.getElementById('stat-correlation').textContent = secondOrderCorr.toFixed(4);
                document.getElementById('stat-points').textContent = results.length;

                // Calculate falsified/insignificant rate
                const insignificantCount = results.filter(r => !r.is_significant).length;
                const falsifiedRate = results.length > 0 ? (insignificantCount / results.length) * 100 : 0;
                document.getElementById('stat-falsified').textContent = `${falsifiedRate.toFixed(1)}%`;

                // Process timeline dataset
                const labels = results.map(r => {
                    const date = new Date(r.timestamp);
                    return date.toLocaleDateString() + ' ' + date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
                });
                const optimalLags = results.map(r => r.optimal_lag);
                const correlations = results.map(r => r.correlation);
                const volatilities = results.map(r => r.volatility);

                // Build optimal lag frequency distribution
                const lagFreq = {};
                for (let i = 0; i <= exp.max_lag; i++) {
                    lagFreq[i] = 0;
                }
                optimalLags.forEach(lag => {
                    if (lagFreq[lag] !== undefined) {
                        lagFreq[lag]++;
                    }
                });

                // Update charts
                renderTimelineChart(labels, optimalLags, volatilities, results);
                renderCCFChart(fullCCF);
                renderDistributionChart(lagFreq);
            })
            .catch(err => {
                console.error('Error fetching results:', err);
            });
    }

    function renderTimelineChart(labels, optimalLags, volatilities, rawResults) {
        const ctx = document.getElementById('timeline-chart').getContext('2d');
        if (timelineChart) {
            timelineChart.destroy();
        }

        // Color coding: Red = Volatility Alert (Unstable), Slate = Falsified (Insignificant), Green = Validated (Significant)
        const pointColors = rawResults.map(r => {
            if (!r.is_stable) return '#f43f5e';         // Volatility spike (unstable)
            if (!r.is_significant) return '#94a3b8';    // Insignificant (falsified)
            return '#10b981';                           // Statistically significant (validated)
        });
        const pointRadii = rawResults.map(r => {
            if (!r.is_stable) return 6;
            if (!r.is_significant) return 4;
            return 5;
        });

        timelineChart = new Chart(ctx, {
            type: 'line',
            data: {
                labels: labels,
                datasets: [
                    {
                        label: 'Optimal Lag (Steps)',
                        data: optimalLags,
                        borderColor: '#6366f1',
                        backgroundColor: 'rgba(99, 102, 241, 0.1)',
                        borderWidth: 2,
                        pointBackgroundColor: pointColors,
                        pointBorderColor: pointColors,
                        pointRadius: pointRadii,
                        tension: 0.15,
                        yAxisID: 'y'
                    },
                    {
                        label: 'Rolling Volatility (StdDev)',
                        data: volatilities,
                        borderColor: '#f59e0b',
                        borderDash: [5, 5],
                        borderWidth: 1.5,
                        pointRadius: 0,
                        fill: false,
                        yAxisID: 'yVol'
                    }
                ]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                interaction: {
                    mode: 'index',
                    intersect: false
                },
                scales: {
                    x: {
                        grid: {
                            color: 'rgba(255, 255, 255, 0.04)'
                        },
                        ticks: {
                            color: '#94a3b8',
                            font: { size: 10 }
                        }
                    },
                    y: {
                        type: 'linear',
                        display: true,
                        position: 'left',
                        title: {
                            display: true,
                            text: 'Optimal Lead-Lag Steps',
                            color: '#94a3b8',
                            font: { family: 'Outfit', size: 12 }
                        },
                        grid: {
                            color: 'rgba(255, 255, 255, 0.04)'
                        },
                        ticks: {
                            color: '#94a3b8'
                        }
                    },
                    yVol: {
                        type: 'linear',
                        display: true,
                        position: 'right',
                        title: {
                            display: true,
                            text: 'Asset Return Volatility',
                            color: '#94a3b8',
                            font: { family: 'Outfit', size: 12 }
                        },
                        grid: {
                            drawOnChartArea: false // prevent grid overlaps
                        },
                        ticks: {
                            color: '#f59e0b'
                        }
                    }
                },
                plugins: {
                    legend: {
                        labels: {
                            color: '#f8fafc',
                            font: { family: 'Inter' }
                        }
                    },
                    tooltip: {
                        callbacks: {
                            footer: (tooltipItems) => {
                                const index = tooltipItems[0].dataIndex;
                                const item = rawResults[index];
                                return [
                                    `Pearson R: ${item.correlation.toFixed(4)}`,
                                    `Stable Regime: ${item.is_stable ? 'Yes' : 'No (Volatility Alert)'}`,
                                    `T-Stat: ${item.t_statistic !== undefined ? item.t_statistic.toFixed(3) : 'N/A'}`,
                                    `P-Value: ${item.p_value !== undefined ? (item.p_value < 0.001 ? item.p_value.toExponential(3) : item.p_value.toFixed(4)) : 'N/A'}`,
                                    `Hypothesis: ${item.is_significant ? 'VALIDATED (Significant)' : 'FALSIFIED (Insignificant)'}`
                                ].join('\n');
                            }
                        }
                    }
                }
            }
        });
    }

    function renderCCFChart(fullCCF) {
        const ctx = document.getElementById('ccf-chart').getContext('2d');
        if (ccfChart) {
            ccfChart.destroy();
        }

        const labels = fullCCF.map(item => `Lag ${item.lag}`);
        const correlationValues = fullCCF.map(item => item.correlation);

        // Find peak index to highlight it in green
        const maxAbs = Math.max(...correlationValues.map(v => Math.abs(v)));
        const backgroundColors = correlationValues.map(v => 
            Math.abs(v) === maxAbs ? '#10b981' : 'rgba(99, 102, 241, 0.6)'
        );

        ccfChart = new Chart(ctx, {
            type: 'bar',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Pearson Correlation Coeff',
                    data: correlationValues,
                    backgroundColor: backgroundColors,
                    borderWidth: 0,
                    borderRadius: 4
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                scales: {
                    x: {
                        grid: { color: 'rgba(255, 255, 255, 0.04)' },
                        ticks: { color: '#94a3b8' }
                    },
                    y: {
                        grid: { color: 'rgba(255, 255, 255, 0.04)' },
                        ticks: { color: '#94a3b8' },
                        suggestedMin: -1,
                        suggestedMax: 1
                    }
                },
                plugins: {
                    legend: { display: false }
                }
            }
        });
    }

    function renderDistributionChart(lagFreq) {
        const ctx = document.getElementById('distribution-chart').getContext('2d');
        if (distributionChart) {
            distributionChart.destroy();
        }

        const labels = Object.keys(lagFreq).map(lag => `Lag ${lag}`);
        const frequencies = Object.values(lagFreq);

        distributionChart = new Chart(ctx, {
            type: 'doughnut',
            data: {
                labels: labels,
                datasets: [{
                    data: frequencies,
                    backgroundColor: [
                        '#6366f1',
                        '#10b981',
                        '#f59e0b',
                        '#8b5cf6',
                        '#ec4899',
                        '#3b82f6',
                        '#14b8a6',
                        '#f43f5e'
                    ],
                    borderWidth: 0
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: {
                        position: 'right',
                        labels: {
                            color: '#94a3b8',
                            boxWidth: 12,
                            font: { size: 11 }
                        }
                    }
                }
            }
        });
    }
});
