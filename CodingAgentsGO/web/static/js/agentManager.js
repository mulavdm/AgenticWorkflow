/**
 * AgentManager Module
 * Handles agent discovery, editing, and persistence.
 */
export class AgentManager {
    constructor() {
        this.agents = {};
        this.currentAgentRole = null;
        this.availableModels = [];
        
        this.grid = document.getElementById('agents-grid');
        this.editor = document.getElementById('agent-editor-panel');
        this.saveBtn = document.getElementById('btn-save-agent');
        this.backBtn = document.getElementById('btn-back-to-grid');
        
        this.initEventListeners();
    }

    initEventListeners() {
        this.saveBtn.addEventListener('click', () => this.saveCurrentAgent());
        this.backBtn.addEventListener('click', () => this.closeEditor());
        
        document.getElementById('btn-reset-agent').addEventListener('click', () => this.resetAgent());
        document.getElementById('btn-save-global').addEventListener('click', () => this.saveGlobalConfig());
        document.getElementById('btn-sync-all').addEventListener('click', () => this.syncAllAgents());
    }

    async loadAgents() {
        try {
            const resp = await fetch('/api/agents');
            this.agents = await resp.json();
            this.renderGrid();
        } catch (err) {
            console.error('Failed to load agents:', err);
        }
    }

    async loadGlobalConfig() {
        try {
            console.log("[AgentManager] Loading Global Config...");
            const resp = await fetch('/api/config');
            const cfg = await resp.json();
            
            const modeEl = document.getElementById('global-mode');
            if (modeEl) modeEl.value = cfg.default_mode || 'local';
            
            // Populate Global Model Selectors
            this.populateGlobalSelector('global-ollama-model', 'ollama', cfg.ollama_model);
            this.populateGlobalSelector('global-lmstudio-model', 'lmstudio', cfg.lmstudio_model);
            this.populateGlobalSelector('global-openai-model', 'openai', cfg.openai_model);
            
        } catch (err) {
            console.error('[AgentManager] Failed to load global config:', err);
        }
    }

    populateGlobalSelector(elementId, provider, currentValue) {
        const select = document.getElementById(elementId);
        if (!select) return;
        select.innerHTML = '';
        
        // Add current value if not in list
        const models = this.availableModels.filter(m => m.provider === provider || (provider === 'openai' && (m.provider === 'cloud' || m.provider === 'llamacpp')));
        console.log(`[AgentManager] Populating ${elementId} with ${models.length} models for ${provider}`);

        models.forEach(m => {
            const opt = document.createElement('option');
            opt.value = m.id;
            opt.textContent = m.id;
            if (m.id === currentValue) opt.selected = true;
            select.appendChild(opt);
        });

        if (currentValue && !models.find(m => m.id === currentValue)) {
            const opt = document.createElement('option');
            opt.value = currentValue;
            opt.textContent = `${currentValue} (Current)`;
            opt.selected = true;
            select.prepend(opt);
        }
    }

    async saveGlobalConfig() {
        const cfg = {
            default_mode: document.getElementById('global-mode').value,
            ollama_model: document.getElementById('global-ollama-model').value,
            lmstudio_model: document.getElementById('global-lmstudio-model').value,
            openai_model: document.getElementById('global-openai-model').value
        };

        try {
            const resp = await fetch('/api/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(cfg)
            });
            if (resp.ok) {
                this.showNotification('Global Matrix Resynced');
                this.loadAgents(); 
            }
        } catch (err) {
            console.error('Failed to save global config:', err);
        }
    }

    async syncAllAgents() {
        if (!confirm('Are you sure you want to reset all specialists to current global defaults? This will wipe all manual overrides.')) return;
        
        try {
            // We can iterate over agents and reset them
            const roles = Object.keys(this.agents);
            for (const role of roles) {
                await fetch(`/api/agents/${role}/reset`, { method: 'POST' });
            }
            this.loadAgents();
            this.showNotification('All Specialists Normalized');
        } catch (err) {
            console.error('Sync failed:', err);
        }
    }

    async loadModels() {
        try {
            const resp = await fetch('/api/models');
            this.availableModels = await resp.json();
        } catch (err) {
            console.error('[AgentManager] Failed to load models:', err);
        }
    }

    renderGrid() {
        this.grid.innerHTML = '';
        Object.entries(this.agents).forEach(([role, agent]) => {
            const card = document.createElement('div');
            const color = this.getRoleColor(role);
            const rgb = this.getRoleRGB(role);
            
            card.className = `agent-card ${agent.is_custom ? 'pinned' : ''}`;
            card.setAttribute('role', 'button');
            card.setAttribute('aria-label', `Configure specialist: ${agent.name || role}`);
            card.setAttribute('tabindex', '0');
            card.style.setProperty('--role-color', color);
            card.style.setProperty('--role-rgb', rgb);
            
            const inheritanceLabel = agent.is_custom ? 'Manual Override' : 'Inherited from Global';
            
            card.innerHTML = `
                ${agent.is_custom ? '<div class="lock-icon" title="Pinned: Ignores Global Defaults">🔒</div>' : ''}
                <div class="role-badge" style="--role-color: ${color}; --role-rgb: ${rgb}">
                    ${role}
                </div>
                <h3>${agent.name || role}</h3>
                <div class="model-info">
                    ${agent.model} 
                    <span style="opacity: 0.4; font-size: 0.6rem; margin-left: 5px;">[${inheritanceLabel}]</span>
                </div>
                <div class="prompt-preview">${this.escapeHtml(agent.system_prompt)}</div>
            `;
            card.addEventListener('click', () => this.openEditor(role));
            card.addEventListener('keydown', (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    this.openEditor(role);
                }
            });
            this.grid.appendChild(card);
        });
    }

    async openEditor(role) {
        this.currentAgentRole = role;
        const agent = this.agents[role];
        
        document.getElementById('editing-agent-name').textContent = `Editing Specialist: ${agent.name || role}`;
        document.getElementById('edit-agent-name').value = agent.name || '';
        document.getElementById('edit-agent-prompt').value = agent.directive || agent.system_prompt || '';
        document.getElementById('edit-agent-temp').value = agent.temperature || 0.7;
        document.getElementById('edit-agent-tokens').value = agent.max_tokens || 4096;
        
        // Render Tool Chips
        const toolContainer = document.getElementById('edit-agent-tools');
        if (toolContainer) {
            toolContainer.innerHTML = '';
            if (agent.tools && agent.tools.length > 0) {
                agent.tools.forEach(tool => {
                    const chip = document.createElement('span');
                    chip.className = 'tool-chip';
                    chip.textContent = tool;
                    toolContainer.appendChild(chip);
                });
            } else {
                toolContainer.innerHTML = '<span style="opacity: 0.3; font-size: 0.8rem;">No tools bound.</span>';
            }
        }

        // Fetch and Render Constitution
        const constitutionViewer = document.getElementById('edit-agent-constitution');
        if (constitutionViewer) {
            constitutionViewer.textContent = 'Loading core identity...';
            try {
                // We pass the raw role string, backend handles mapping to pkg
                const resp = await fetch(`/api/agents/constitution/${role}`);
                if (resp.ok) {
                    const md = await resp.text();
                    if (window.marked) {
                        constitutionViewer.innerHTML = marked.parse(md);
                    } else {
                        constitutionViewer.textContent = md;
                    }
                } else {
                    constitutionViewer.textContent = 'Constitution not found for this specialist package.';
                }
            } catch (err) {
                console.error('Failed to load constitution:', err);
                constitutionViewer.textContent = 'Failed to load constitution.';
            }
        }
        
        // Populate model selector
        const modelSelect = document.getElementById('edit-agent-model');
        if (modelSelect) {
            modelSelect.innerHTML = '';
            this.availableModels.forEach(m => {
                const opt = document.createElement('option');
                opt.value = m.id;
                opt.textContent = `${m.provider}: ${m.id} ${m.is_loaded ? '(Loaded)' : ''}`;
                if (m.id === agent.model) opt.selected = true;
                modelSelect.appendChild(opt);
            });

            if (!this.availableModels.find(m => m.id === agent.model)) {
                const opt = document.createElement('option');
                opt.value = agent.model;
                opt.textContent = `Current: ${agent.model}`;
                opt.selected = true;
                modelSelect.prepend(opt);
            }
        }

        // Show/Hide Reset button
        const resetBtn = document.getElementById('btn-reset-agent');
        if (resetBtn) resetBtn.style.display = agent.is_custom ? 'block' : 'none';

        this.editor.classList.remove('hidden');
        this.editor.setAttribute('aria-hidden', 'false');
        this.editor.focus();
    }

    closeEditor() {
        this.editor.classList.add('hidden');
        this.editor.setAttribute('aria-hidden', 'true');
        this.currentAgentRole = null;
    }

    async resetAgent() {
        if (!this.currentAgentRole) return;
        
        try {
            const resp = await fetch(`/api/agents/${this.currentAgentRole}/reset`, {
                method: 'POST'
            });
            if (resp.ok) {
                this.closeEditor();
                this.loadAgents();
            }
        } catch (err) {
            console.error('Reset failed:', err);
        }
    }

    async saveCurrentAgent() {
        if (!this.currentAgentRole) return;
        
        const updated = {
            ...this.agents[this.currentAgentRole],
            name: document.getElementById('edit-agent-name').value,
            model: document.getElementById('edit-agent-model').value,
            directive: document.getElementById('edit-agent-prompt').value,
            temperature: parseFloat(document.getElementById('edit-agent-temp').value),
            max_tokens: parseInt(document.getElementById('edit-agent-tokens').value)
        };

        try {
            this.saveBtn.textContent = 'Syncing...';
            this.saveBtn.disabled = true;
            
            const resp = await fetch(`/api/agents/${this.currentAgentRole}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(updated)
            });
            
            if (resp.ok) {
                this.agents[this.currentAgentRole] = updated;
                this.renderGrid();
                this.closeEditor();
                this.showNotification('Specialist Matrix Updated');
            } else {
                throw new Error(await resp.text());
            }
        } catch (err) {
            console.error('Save failed:', err);
            alert('Protocol Error: ' + err.message);
        } finally {
            this.saveBtn.textContent = 'Commit Changes';
            this.saveBtn.disabled = false;
        }
    }

    getRoleColor(role) {
        const colors = {
            'analyst': '#60A5FA',
            'planner': '#F59E0B',
            'critic': '#EF4444',
            'security': '#8B5CF6',
            'coder': '#3B82F6',
            'synthesizer': '#10B981'
        };
        return colors[role] || '#9CA3AF';
    }

    getRoleRGB(role) {
        const rgbs = {
            'analyst': '96, 165, 250',
            'planner': '245, 158, 11',
            'critic': '239, 68, 68',
            'security': '139, 92, 246',
            'coder': '59, 130, 246',
            'synthesizer': '16, 185, 129'
        };
        return rgbs[role] || '156, 163, 175';
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    showNotification(msg) {
        // Simple notification logic
        console.log('Notification:', msg);
        // You could add a UI toast here
    }
}
