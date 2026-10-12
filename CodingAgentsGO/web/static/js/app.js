import { AgentManager } from './agentManager.js';

const transcriptFeed = document.getElementById('transcript-feed');
const currentPhaseLabel = document.getElementById('current-phase');
const memoryList = document.getElementById('memory-list');
const btnExecute = document.getElementById('btn-execute');
const userInput = document.getElementById('user-input');
const runList = document.getElementById('run-list');
const runDetailTitle = document.getElementById('run-detail-title');
const runDetailContent = document.getElementById('run-detail-content');
const approvalList = document.getElementById('approval-list');
const btnRefreshRuns = document.getElementById('btn-refresh-runs');
const btnCancelRun = document.getElementById('btn-cancel-run');
const btnProviderProbe = document.getElementById('btn-provider-probe');
const providerProbeUrl = document.getElementById('provider-probe-url');
const providerProbeResult = document.getElementById('provider-probe-result');
const runSubtabs = document.querySelectorAll('.subtab-btn');
const btnRefreshOps = document.getElementById('btn-refresh-ops');
const healthSummary = document.getElementById('health-summary');
const healthDetails = document.getElementById('health-details');
const indexerSummary = document.getElementById('indexer-summary');
const indexerDetails = document.getElementById('indexer-details');
const capabilitiesSummary = document.getElementById('capabilities-summary');
const capabilitiesDetails = document.getElementById('capabilities-details');
const policySummary = document.getElementById('policy-summary');
const policyDetails = document.getElementById('policy-details');
const opsRawJson = document.getElementById('ops-raw-json');
let selectedRunID = '';
let selectedRunTab = 'timeline';

// Initialize Agent Manager
const agentManager = new AgentManager();

// Tab Switching Logic
const tabButtons = document.querySelectorAll('.tab-btn');
const tabContents = document.querySelectorAll('.tab-content');

tabButtons.forEach(btn => {
    btn.addEventListener('click', () => {
        const targetTab = btn.getAttribute('data-tab');
        
        // Update Buttons
        tabButtons.forEach(b => {
            b.classList.remove('active');
            b.setAttribute('aria-selected', 'false');
        });
        btn.classList.add('active');
        btn.setAttribute('aria-selected', 'true');
        
        // Update Content
        tabContents.forEach(content => {
            content.classList.remove('active');
            content.setAttribute('aria-hidden', 'true');
            if (content.id === `tab-${targetTab}`) {
                content.classList.add('active');
                content.setAttribute('aria-hidden', 'false');
                // Move focus to content for accessibility
                content.setAttribute('tabindex', '-1');
                content.focus();
            }
        });

        // Trigger tab-specific logic
        if (targetTab === 'agents') {
            (async () => {
                try {
                    console.log("[App] Initializing Agent Lab...");
                    await agentManager.loadModels();
                    await agentManager.loadAgents();
                    await agentManager.loadGlobalConfig();
                    console.log("[App] Agent Lab Initialized.");
                } catch (err) {
                    console.error("[App] Failed to initialize Agent Lab:", err);
                }
            })();
        }
        if (targetTab === 'runs') {
            loadRuns();
        }
        if (targetTab === 'operations') {
            loadOperations();
        }
    });
});

// Initialize SSE Connection
function initSSE() {
    console.log("Connecting to SSE...");
    const eventSource = new EventSource('/api/stream');

    eventSource.onopen = () => {
        console.log("SSE Connected");
        currentPhaseLabel.innerText = "Connected";
    };

    eventSource.onmessage = (event) => {
        let data;
        try {
            data = JSON.parse(event.data);
        } catch (err) {
            console.error("Invalid SSE payload:", err);
            return;
        }
        console.log("SSE Event Received:", data);
        
        switch (data.type) {
            case 'phase':
                handlePhaseChange(data.payload);
                break;
            case 'message':
                addMessageToFeed(data.payload);
                break;
            case 'observation':
                addObservationToFeed(data.payload);
                break;
            case 'session_error':
                addMessageToFeed(data.payload);
                currentPhaseLabel.innerText = "Error";
                break;
        }
    };

    eventSource.onerror = (err) => {
        console.error("SSE Error:", err);
        currentPhaseLabel.innerText = "Reconnecting...";
    };
}

const agentSelector = document.getElementById('agent-selector');

btnExecute.addEventListener('click', async () => {
    let request = userInput.value;
    const role = agentSelector.value;
    const modeSelector = document.getElementById('mode-selector');
    const mode = modeSelector ? modeSelector.value : 'local';
    const modelSelector = document.getElementById('model-selector');
    const model = modelSelector ? modelSelector.value : '';

    if (!request) return;

    // Automatically prepend the mode tag for the coordinator
    // Use @provider:model if a specific model is selected
    let tag = `@${mode}`;
    if (model) {
        tag = `@${mode}:${model}`;
    }
    let fullRequest = `${tag} ${request}`;
    
    const shadowToggle = document.getElementById('shadow-toggle');
    if (shadowToggle && shadowToggle.checked) {
        fullRequest = `@shadow ${fullRequest}`;
    }

    const councilToggle = document.getElementById('council-toggle');
    if (councilToggle && councilToggle.checked) {
        fullRequest = `@council ${fullRequest}`;
    }

    console.log("[UI] Sending request to orchestrator:", fullRequest);

    btnExecute.disabled = true;
    btnExecute.innerText = 'Processing...';
    
    // Optimistically add user message to feed (original text)
    addMessageToFeed({
        role: 'user',
        content: request
    });

    try {
        const graphData = window.getGraphData ? window.getGraphData() : null;
        const resp = await fetch('/api/execute', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ prompt: fullRequest, role: role, graph: graphData })
        });
        
        if (!resp.ok) throw new Error("Failed to trigger execution");
        const body = await resp.json();
        if (body.run_id) {
            selectedRunID = body.run_id;
            currentPhaseLabel.innerText = `Run ${body.run_id}`;
            loadRuns();
            loadRunDetail(body.run_id, selectedRunTab);
        }
        
        userInput.value = '';
    } catch (err) {
        console.error('Execution failed:', err);
        addMessageToFeed({
            role: 'system',
            content: "Error: " + err.message
        });
    } finally {
        btnExecute.disabled = false;
        btnExecute.innerText = 'Execute';
    }
});

btnRefreshRuns?.addEventListener('click', loadRuns);
btnRefreshOps?.addEventListener('click', loadOperations);

btnCancelRun?.addEventListener('click', async () => {
    if (!selectedRunID) return;
    btnCancelRun.disabled = true;
    try {
        const resp = await fetch('/api/execute/cancel', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ run_id: selectedRunID })
        });
        if (!resp.ok) throw new Error(await resp.text());
        await loadRuns();
        await loadRunDetail(selectedRunID, selectedRunTab);
    } catch (err) {
        renderDataView([{ error: err.message }]);
    }
});

runSubtabs.forEach(btn => {
    btn.addEventListener('click', () => {
        runSubtabs.forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        selectedRunTab = btn.dataset.runTab || 'timeline';
        if (selectedRunID) {
            loadRunDetail(selectedRunID, selectedRunTab);
        }
    });
});

btnProviderProbe?.addEventListener('click', async () => {
    const url = providerProbeUrl?.value?.trim();
    if (!url) return;
    providerProbeResult.textContent = 'Probing...';
    try {
        const resp = await fetch('/api/providers/probe', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ url })
        });
        const result = await resp.json();
        renderProviderProbe(result);
    } catch (err) {
        providerProbeResult.textContent = err.message;
    }
});

function renderProviderProbe(result) {
    providerProbeResult.replaceChildren();
    const summary = document.createElement('div');
    summary.className = `provider-probe-summary ${result.ok ? 'ok' : 'error'}`;

    const title = document.createElement('strong');
    title.textContent = `${result.provider || 'provider'} · ${result.ok ? 'ready' : 'unavailable'}`;
    summary.appendChild(title);

    const caps = result.capabilities || {};
    const labels = [
        ['system', caps.system_messages],
        ['developer', caps.developer_messages],
        ['json schema', caps.json_schema],
        ['streaming', caps.streaming],
        caps.context_size ? [`ctx ${caps.context_size}`, true] : null,
    ].filter(Boolean);
    labels.forEach(([label, enabled]) => {
        const chip = document.createElement('span');
        chip.className = `capability-chip ${enabled ? 'enabled' : 'disabled'}`;
        chip.textContent = label;
        summary.appendChild(chip);
    });

    const raw = document.createElement('pre');
    raw.textContent = JSON.stringify(result, null, 2);
    providerProbeResult.append(summary, raw);
}

function handlePhaseChange(phase) {
    currentPhaseLabel.innerText = phase;
    
    if (window.activateNodeByType) {
        window.activateNodeByType(phase);
    }
}

function addObservationToFeed(obs) {
    // Hide tool observations from the public transcript for a cleaner "Premium" feel.
    console.log("Internal Observation:", obs.tool);
}

function addMessageToFeed(msg) {
    // Only render messages marked as visible (User or Synthesizer)
    if (msg.visible === false) {
        console.log(`Hidden Reasoning (${msg.role}):`, String(msg.content || '').substring(0, 50) + "...");
        return;
    }

    const card = document.createElement('div');
    card.className = `message-card ${msg.role}`;

    let displayName = msg.role === 'user' ? 'You' : msg.role.toUpperCase();
    if (msg.model) {
        displayName += ` (${msg.model})`;
    }

    const header = document.createElement('div');
    header.className = 'message-header';

    const title = document.createElement('h4');
    title.textContent = displayName;
    header.appendChild(title);

    const metadata = document.createElement('div');
    metadata.className = 'metadata';
    if (msg.phase) {
        const phaseBadge = document.createElement('span');
        phaseBadge.className = 'badge phase';
        phaseBadge.textContent = msg.phase;
        metadata.appendChild(phaseBadge);
    }
    header.appendChild(metadata);

    const content = document.createElement('div');
    content.className = 'content';
    renderFormattedContent(content, msg.content || '');

    card.appendChild(header);
    card.appendChild(content);

    if (msg.usage && msg.role !== 'user') {
        card.appendChild(renderUsage(msg.usage));
    }
    
    // Add ARIA attributes for accessibility
    card.setAttribute('role', 'article');
    card.setAttribute('aria-label', `Message from ${msg.role}`);
    if (msg.role === 'user') {
        card.setAttribute('aria-live', 'polite');
    }

    transcriptFeed.prepend(card);
}

function renderUsage(usage) {
    const totalMs = (usage.total_duration / 1000000).toFixed(0);
    const usageEl = document.createElement('div');
    usageEl.className = 'message-usage';

    const tokens = document.createElement('span');
    tokens.textContent = `Tokens: ${usage.prompt_eval_count} in / ${usage.eval_count} out`;

    const speed = document.createElement('span');
    speed.textContent = `Speed: ${totalMs}ms`;

    usageEl.append(tokens, speed);
    return usageEl;
}

function renderFormattedContent(container, text) {
    const parts = String(text).split(/```([\s\S]*?)```/g);

    parts.forEach((part, index) => {
        if (part === '') return;

        if (index % 2 === 1) {
            const pre = document.createElement('pre');
            const code = document.createElement('code');
            code.textContent = part;
            pre.appendChild(code);
            container.appendChild(pre);
            return;
        }

        appendTextWithBreaks(container, part);
    });
}

function appendTextWithBreaks(container, text) {
    String(text).split('\n').forEach((line, index) => {
        if (index > 0) {
            container.appendChild(document.createElement('br'));
        }
        container.appendChild(document.createTextNode(line));
    });
}

async function loadRuns() {
    if (!runList) return;
    try {
        const resp = await fetch('/api/runs?limit=25');
        const runs = await resp.json();
        runList.replaceChildren();
        runs.forEach(run => {
            const card = document.createElement('button');
            card.className = `run-card ${run.status || ''}`;
            if (run.run_id === selectedRunID) {
                card.classList.add('active');
            }
            card.type = 'button';

            const title = document.createElement('strong');
            title.textContent = run.run_id;
            const meta = document.createElement('span');
            meta.textContent = `${run.status || 'unknown'} · ${run.workflow_name || 'workflow'}`;
            card.append(title, meta);
            card.addEventListener('click', () => {
                selectedRunID = run.run_id;
                loadRunDetail(selectedRunID, selectedRunTab);
                loadRuns();
            });
            runList.appendChild(card);
        });
    } catch (err) {
        runList.textContent = err.message;
    }
}

async function loadRunDetail(runID, tab) {
    if (!runDetailContent || !runID) return;
    selectedRunID = runID;
    runDetailTitle.textContent = runID;
    btnCancelRun.disabled = false;
    const resource = tab === 'timeline' ? 'timeline' : tab;
    try {
        const resp = await fetch(`/api/runs/${encodeURIComponent(runID)}/${resource}`);
        const payload = await resp.json();
        renderDataView(payload);
        await loadApprovals(runID);
    } catch (err) {
        renderDataView([{ error: err.message }]);
    }
}

async function loadApprovals(runID) {
    if (!approvalList) return;
    try {
        const resp = await fetch(`/api/runs/${encodeURIComponent(runID)}/approvals`);
        const approvals = await resp.json();
        approvalList.replaceChildren();
        if (!approvals.length) {
            approvalList.textContent = 'No approvals.';
            return;
        }
        approvals.forEach(approval => approvalList.appendChild(renderApprovalCard(approval)));
    } catch (err) {
        approvalList.textContent = err.message;
    }
}

async function loadOperations() {
    if (!healthSummary || !indexerSummary || !capabilitiesSummary || !policySummary) return;
    setOpsLoading();
    try {
        const [health, indexer, policy] = await Promise.all([
            fetchJSON('/api/health'),
            fetchJSON('/api/indexer/status'),
            fetchJSON('/api/tool-policy'),
        ]);
        renderOperations(health, indexer, policy);
    } catch (err) {
        renderOpsError(err);
    }
}

async function fetchJSON(url) {
    const resp = await fetch(url);
    if (!resp.ok) {
        throw new Error(`${url}: ${await resp.text()}`);
    }
    return resp.json();
}

function setOpsLoading() {
    healthSummary.textContent = 'Checking...';
    indexerSummary.textContent = 'Checking...';
    capabilitiesSummary.textContent = 'Checking...';
    policySummary.textContent = 'Checking...';
}

function renderOperations(health, indexer, policy) {
    renderStatusSummary(healthSummary, health?.ok ? 'Ready' : 'Needs attention', health?.ok ? 'ok' : 'warn');
    renderOpsRows(healthDetails, [
        ['Memory', health?.memory_present ? 'available' : 'missing'],
        ['Blackboard schema', formatValue(health?.blackboard_schema_version || 'unavailable')],
        ['Log directory', formatValue(health?.logger?.log_dir)],
        ['Active log', formatValue(health?.logger?.active_log_path)],
        ['Log level', formatValue(health?.logger?.current_level)],
        ['Last log error', formatValue(health?.logger?.last_write_error || 'none')],
    ]);

    const capabilities = Array.isArray(health?.capabilities) ? health.capabilities : [];
    const capabilityFailures = capabilities.filter(cap => ['failed', 'cancelled', 'missing'].includes(String(cap.status || '').toLowerCase()));
    renderStatusSummary(
        capabilitiesSummary,
        capabilities.length ? `${capabilities.length} registered` : 'None registered',
        capabilityFailures.length ? 'warn' : 'ok'
    );
    renderOpsRows(capabilitiesDetails, capabilityRows(capabilities));

    renderStatusSummary(indexerSummary, indexer?.running ? 'Indexing' : 'Idle', indexer?.last_error ? 'warn' : 'ok');
    renderOpsRows(indexerDetails, [
        ['Files scanned', formatValue(indexer?.files_scanned)],
        ['Files indexed', formatValue(indexer?.files_indexed)],
        ['Files skipped', formatValue(indexer?.files_skipped)],
        ['Chunks indexed', formatValue(indexer?.chunks_indexed)],
        ['Last started', formatTimestamp(indexer?.last_started_at)],
        ['Last finished', formatTimestamp(indexer?.last_finished_at)],
        ['Last error', formatValue(indexer?.last_error || 'none')],
    ]);

    const safePrefixes = Array.isArray(policy?.safe_write_prefixes) ? policy.safe_write_prefixes.join(', ') : 'default';
    renderStatusSummary(policySummary, 'Conservative defaults', 'ok');
    renderOpsRows(policyDetails, [
        ['Safe write prefixes', safePrefixes],
        ['Coderunner approval threshold', `${formatValue(policy?.coderunner_approval_threshold_ms)} ms`],
        ['Docker', 'approval required'],
        ['Merge and promotion', 'approval required'],
    ]);

    if (opsRawJson) {
        opsRawJson.textContent = JSON.stringify({ health, indexer, policy }, null, 2);
    }
}

function renderOpsError(err) {
    renderStatusSummary(healthSummary, 'Unavailable', 'error');
    healthDetails.textContent = err.message;
    indexerSummary.textContent = '';
    indexerDetails.textContent = '';
    capabilitiesSummary.textContent = '';
    capabilitiesDetails.textContent = '';
    policySummary.textContent = '';
    policyDetails.textContent = '';
    if (opsRawJson) {
        opsRawJson.textContent = JSON.stringify({ error: err.message }, null, 2);
    }
}

function renderStatusSummary(container, text, state) {
    container.replaceChildren();
    const badge = document.createElement('span');
    badge.className = `ops-status ${state || 'ok'}`;
    badge.textContent = text;
    container.appendChild(badge);
}

function capabilityRows(capabilities) {
    if (!capabilities.length) {
        return [['Registered runtimes', 'none']];
    }
    return capabilities.map(cap => {
        const bits = [cap.status || 'unknown'];
        if (cap.pid) bits.push(`pid ${cap.pid}`);
        if (cap.endpoint_url) bits.push(cap.endpoint_url);
        if (cap.error) bits.push(cap.error);
        return [cap.name || 'capability', bits.join(' | ')];
    });
}

function renderOpsRows(container, rows) {
    container.replaceChildren();
    rows.forEach(([label, value]) => {
        const row = document.createElement('div');
        row.className = 'ops-row';
        const key = document.createElement('span');
        key.textContent = label;
        const val = document.createElement('strong');
        val.textContent = value;
        row.append(key, val);
        container.appendChild(row);
    });
}

function formatValue(value) {
    if (value === undefined || value === null || value === '') return 'unavailable';
    return String(value);
}

function formatTimestamp(value) {
    if (!value || value === '0001-01-01T00:00:00Z') return 'never';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleString();
}

function renderApprovalCard(approval) {
    const card = document.createElement('div');
    card.className = `approval-card ${approval.status || ''}`;

    const title = document.createElement('strong');
    title.textContent = `${approval.tool || 'tool'} · ${approval.status || 'pending'}`;

    const reason = document.createElement('p');
    reason.textContent = approval.reason || '';

    const mode = document.createElement('span');
    mode.className = 'approval-mode';
    mode.textContent = approval.resume_mode || 'decision-only';

    const args = document.createElement('code');
    args.textContent = approval.args || '';

    card.append(title, mode, reason, args);
    if (approval.status === 'pending') {
        const actions = document.createElement('div');
        actions.className = 'approval-actions';
        const approve = document.createElement('button');
        approve.className = 'small-btn';
        approve.textContent = 'Approve';
        approve.addEventListener('click', () => decideApproval(approval.id, 'approved'));
        const deny = document.createElement('button');
        deny.className = 'small-btn danger';
        deny.textContent = 'Deny';
        deny.addEventListener('click', () => decideApproval(approval.id, 'denied'));
        actions.append(approve, deny);
        card.appendChild(actions);
    }
    return card;
}

async function decideApproval(approvalID, decision) {
    if (!selectedRunID || !approvalID) return;
    if (approvalList) {
        approvalList.setAttribute('aria-busy', 'true');
    }
    try {
        const resp = await fetch(`/api/runs/${encodeURIComponent(selectedRunID)}/approvals/${encodeURIComponent(approvalID)}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ decision })
        });
        if (!resp.ok) {
            renderDataView([{ error: await resp.text() }]);
            return;
        }
        await loadApprovals(selectedRunID);
        await loadRunDetail(selectedRunID, selectedRunTab);
    } finally {
        if (approvalList) {
            approvalList.removeAttribute('aria-busy');
        }
    }
}

function renderDataView(payload) {
    if (!runDetailContent) return;
    runDetailContent.replaceChildren();

    // Render messages subtab as interactive cards with sliding toggles
    if (selectedRunTab === 'messages' && Array.isArray(payload)) {
        if (payload.length === 0) {
            runDetailContent.textContent = "No messages in this run.";
            return;
        }

        payload.forEach(msg => {
            const card = document.createElement('div');
            card.className = `message-card ${msg.role} ${msg.visible ? '' : 'hidden-from-mem'}`;

            const header = document.createElement('div');
            header.className = 'message-header';

            const title = document.createElement('h4');
            title.textContent = msg.role.toUpperCase() + (msg.model ? ` (${msg.model})` : '');
            header.appendChild(title);

            const controls = document.createElement('div');
            controls.className = 'message-controls';

            const toggleLabel = document.createElement('label');
            toggleLabel.className = 'memory-toggle-switch';

            const checkbox = document.createElement('input');
            checkbox.type = 'checkbox';
            checkbox.checked = msg.visible;
            checkbox.addEventListener('change', async () => {
                const action = checkbox.checked ? 'unhide' : 'hide';
                try {
                    const resp = await fetch(`/api/runs/${encodeURIComponent(selectedRunID)}/messages/${encodeURIComponent(msg.id)}/${action}`, {
                        method: 'POST'
                    });
                    if (resp.ok) {
                        card.classList.toggle('hidden-from-mem', !checkbox.checked);
                    } else {
                        checkbox.checked = !checkbox.checked;
                    }
                } catch (err) {
                    checkbox.checked = !checkbox.checked;
                    console.error("Error toggling visibility:", err);
                }
            });

            const slider = document.createElement('span');
            slider.className = 'slider round';

            toggleLabel.append(checkbox, slider);
            controls.appendChild(toggleLabel);
            header.appendChild(controls);

            const content = document.createElement('div');
            content.className = 'content';
            renderFormattedContent(content, msg.content || '');

            card.append(header, content);
            runDetailContent.appendChild(card);
        });
        return;
    }

    const items = Array.isArray(payload) ? payload : [payload];
    items.forEach(item => {
        const pre = document.createElement('pre');
        pre.textContent = JSON.stringify(item, null, 2);
        runDetailContent.appendChild(pre);
    });
}

// Fetch Initial Memory
async function loadMemory() {
    try {
        const resp = await fetch('/api/memory');
        const lessons = await resp.json();

        memoryList.replaceChildren();
        lessons.forEach(lesson => {
            const card = document.createElement('div');
            card.className = 'memory-card';

            const task = document.createElement('div');
            task.className = 'task';
            task.textContent = lesson.task || '';

            const resolution = document.createElement('div');
            resolution.className = 'resolution';

            const label = document.createElement('strong');
            label.textContent = 'Learned: ';
            resolution.appendChild(label);
            resolution.appendChild(document.createTextNode(`${(lesson.resolution || '').substring(0, 150)}...`));

            card.append(task, resolution);
            memoryList.appendChild(card);
        });
    } catch (err) {
        console.error('Failed to load memory:', err);
    }
}

// Model Discovery
let allModels = [];

async function discoverModels() {
    try {
        const resp = await fetch('/api/models');
        allModels = await resp.json();
        updateModelSelector();
    } catch (err) {
        console.error('Failed to discover models:', err);
    }
}

function updateModelSelector() {
    const modeSelector = document.getElementById('mode-selector');
    const modelSelector = document.getElementById('model-selector');
    if (!modeSelector || !modelSelector) return;

    const currentMode = modeSelector.value;
    const filtered = allModels.filter(m => m.provider === currentMode);

    // Keep the first "Auto-Detect" option
    modelSelector.innerHTML = '<option value="">Auto-Detect Model</option>';
    
    filtered.forEach(m => {
        const opt = document.createElement('option');
        opt.value = m.id;
        opt.textContent = m.id;
        modelSelector.appendChild(opt);
    });
}

document.getElementById('mode-selector')?.addEventListener('change', updateModelSelector);

initSSE();
loadMemory();
discoverModels();
setInterval(loadMemory, 30000); 
setInterval(discoverModels, 60000); // Refresh models every minute
setInterval(loadOperations, 30000);
