// Node Editor logic
document.addEventListener("DOMContentLoaded", () => {
    const paletteItems = document.querySelectorAll('.palette-item');
    const editor = document.getElementById('node-editor');
    const nodesContainer = document.getElementById('nodes-container');
    const svgLayer = document.getElementById('connection-layer');
    
    let nodes = {};
    let connections = [];
    let draggedNode = null;
    let offset = { x: 0, y: 0 };
    let connectingFrom = null;

    const roleAliases = {
        analyst: 'context',
        context: 'context',
        context_isolated: 'context',
        planner: 'planner',
        critic: 'critic',
        security: 'redteamer',
        redteam: 'redteamer',
        redteamer: 'redteamer',
        coder: 'coder',
        synthesizer: 'synthesizer'
    };

    function canonicalRole(role) {
        return roleAliases[String(role || '').toLowerCase()] || String(role || '').toLowerCase();
    }

    // Default graph setup based on original viz-panel
    const defaultNodes = [
        { id: 'node-1', type: 'context', name: 'Analyst' },
        { id: 'node-2', type: 'planner', name: 'Planner' },
        { id: 'node-3', type: 'critic', name: 'Critic' },
        { id: 'node-4', type: 'redteamer', name: 'Red Team' },
        { id: 'node-5', type: 'coder', name: 'Coder' },
        { id: 'node-6', type: 'synthesizer', name: 'Synthesizer' },
    ];

    // Wait slightly to ensure layout is ready for port calculations
    setTimeout(() => {
        const rect = editor.getBoundingClientRect();
        const nodeWidth = 150;
        const margin = 30;
        const available = Math.max(rect.width - nodeWidth - margin * 2, 1);
        const y = Math.max(Math.min(rect.height * 0.35, 180), 60);

        defaultNodes.forEach((data, index) => {
            const x = margin + (available * index) / Math.max(defaultNodes.length - 1, 1);
            createNode(data.id, data.type, data.name, x, y);
        });
        
        // Create default connections
        addConnection('node-1', 'node-2'); // Analyst -> Planner
        addConnection('node-2', 'node-3'); // Planner -> Critic
        addConnection('node-2', 'node-5'); // Planner -> Coder (Direct Implementation)
        addConnection('node-3', 'node-4'); // Critic -> Red Team
        addConnection('node-3', 'node-5'); // Critic -> Coder (Approved Plan)
        addConnection('node-4', 'node-5'); // Red Team -> Coder
        addConnection('node-5', 'node-6'); // Coder -> Synthesizer
    }, 100);

    // Drag and Drop from Palette
    paletteItems.forEach(item => {
        item.addEventListener('dragstart', (e) => {
            e.dataTransfer.setData('text/plain', JSON.stringify({
                type: item.dataset.type,
                name: item.innerText
            }));
        });
    });

    editor.addEventListener('dragenter', e => e.preventDefault());
    editor.addEventListener('dragover', e => e.preventDefault());
    editor.addEventListener('drop', e => {
        e.preventDefault();
        try {
            const data = JSON.parse(e.dataTransfer.getData('text/plain'));
            const rect = editor.getBoundingClientRect();
            const x = e.clientX - rect.left;
            const y = e.clientY - rect.top;
            const id = 'node-' + Date.now();
            createNode(id, canonicalRole(data.type), data.name, x - 75, y - 20);
        } catch(err) {
            console.error("Drop ignored", err);
        }
    });

    function createNode(id, type, name, x, y) {
        const node = document.createElement('div');
        node.className = `canvas-node`;
        node.dataset.id = id;
        node.dataset.type = canonicalRole(type);
        node.style.left = `${x}px`;
        node.style.top = `${y}px`;
        node.setAttribute('role', 'group');
        node.setAttribute('aria-label', `Agent node: ${name}`);
        node.setAttribute('tabindex', '0');

        node.innerHTML = `
            <div class="node-header">${name}</div>
            <div class="node-body">
                <div class="port in" data-port="in" role="button" aria-label="Input port for ${name}" tabindex="0"></div>
                <div class="port out" data-port="out" role="button" aria-label="Output port for ${name}" tabindex="0"></div>
            </div>
        `;

        nodesContainer.appendChild(node);
        nodes[id] = { element: node, x, y, inputs: [], outputs: [] };

        // Drag logic
        node.addEventListener('mousedown', e => {
            if (e.button !== 0) return; // Only left click
            if (e.target.classList.contains('port')) return; // Let ports handle their own events
            
            draggedNode = id;
            const rect = node.getBoundingClientRect();
            offset.x = e.clientX - rect.left;
            offset.y = e.clientY - rect.top;
            node.style.zIndex = 100;
        });

        // Port logic
        const outPort = node.querySelector('.port.out');
        const inPort = node.querySelector('.port.in');

        outPort.addEventListener('mousedown', e => {
            e.stopPropagation();
            connectingFrom = id;
        });

        inPort.addEventListener('mouseup', e => {
            e.stopPropagation();
            if (connectingFrom && connectingFrom !== id) {
                addConnection(connectingFrom, id);
            }
            connectingFrom = null;
        });

        // Delete node on right click
        node.addEventListener('contextmenu', e => {
            e.preventDefault();
            deleteNode(id);
        });
    }

    function addConnection(fromId, toId) {
        if (!nodes[fromId] || !nodes[toId]) return;
        // Prevent duplicate connections
        if (connections.find(c => c.from === fromId && c.to === toId)) return;
        
        const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        path.classList.add('connection');
        svgLayer.appendChild(path);
        
        const conn = { from: fromId, to: toId, element: path };
        connections.push(conn);
        nodes[fromId].outputs.push(conn);
        nodes[toId].inputs.push(conn);
        
        updateConnections();
    }

    function deleteNode(id) {
        connections = connections.filter(c => {
            if (c.from === id || c.to === id) {
                c.element.remove();
                return false;
            }
            return true;
        });
        nodes[id].element.remove();
        delete nodes[id];
    }

    document.addEventListener('mousemove', e => {
        if (draggedNode) {
            const rect = editor.getBoundingClientRect();
            // Restrict bounds loosely
            let x = e.clientX - rect.left - offset.x;
            let y = e.clientY - rect.top - offset.y;
            x = Math.max(0, Math.min(x, rect.width - nodes[draggedNode].element.offsetWidth));
            y = Math.max(0, Math.min(y, rect.height - nodes[draggedNode].element.offsetHeight));
            
            nodes[draggedNode].element.style.left = `${x}px`;
            nodes[draggedNode].element.style.top = `${y}px`;
            nodes[draggedNode].x = x;
            nodes[draggedNode].y = y;
            
            updateConnections();
        } else if (connectingFrom) {
            // Draw temporary line
            const rect = editor.getBoundingClientRect();
            const mouseX = e.clientX - rect.left;
            const mouseY = e.clientY - rect.top;
            
            const fromNode = nodes[connectingFrom].element;
            const outPort = fromNode.querySelector('.port.out');
            const portRect = outPort.getBoundingClientRect();
            const fromX = portRect.left + portRect.width / 2 - rect.left;
            const fromY = portRect.top + portRect.height / 2 - rect.top;
            
            let tempPath = svgLayer.querySelector('.temp-connection');
            if (!tempPath) {
                tempPath = document.createElementNS('http://www.w3.org/2000/svg', 'path');
                tempPath.classList.add('connection', 'temp-connection');
                svgLayer.appendChild(tempPath);
            }
            
            const dx = Math.max(Math.abs(mouseX - fromX) / 2, 50);
            const cp1x = fromX + dx;
            const cp1y = fromY;
            const cp2x = mouseX - dx;
            const cp2y = mouseY;
            
            tempPath.setAttribute('d', `M ${fromX} ${fromY} C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${mouseX} ${mouseY}`);
        }
    });

    document.addEventListener('mouseup', () => {
        if (draggedNode) {
            nodes[draggedNode].element.style.zIndex = 10;
            draggedNode = null;
        }
        if (connectingFrom) {
            const temp = svgLayer.querySelector('.temp-connection');
            if (temp) temp.remove();
            connectingFrom = null;
        }
    });

    function updateConnections() {
        const rect = editor.getBoundingClientRect();
        connections.forEach(conn => {
            const fromNode = nodes[conn.from]?.element;
            const toNode = nodes[conn.to]?.element;
            if (!fromNode || !toNode) return;
            
            const outPort = fromNode.querySelector('.port.out');
            const inPort = toNode.querySelector('.port.in');
            
            const outRect = outPort.getBoundingClientRect();
            const inRect = inPort.getBoundingClientRect();
            
            const fromX = outRect.left + outRect.width / 2 - rect.left;
            const fromY = outRect.top + outRect.height / 2 - rect.top;
            const toX = inRect.left + inRect.width / 2 - rect.left;
            const toY = inRect.top + inRect.height / 2 - rect.top;
            
            const dx = Math.max(Math.abs(toX - fromX) / 2, 50);
            const cp1x = fromX + dx;
            const cp1y = fromY;
            const cp2x = toX - dx;
            const cp2y = toY;
            
            conn.element.setAttribute('d', `M ${fromX} ${fromY} C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${toX} ${toY}`);
        });
    }

    const resizeObserver = new ResizeObserver(() => updateConnections());
    resizeObserver.observe(editor);

    // Expose a way to activate nodes by phase
    window.activateNodeByType = function(type) {
        const activeType = canonicalRole(type);
        Object.values(nodes).forEach(n => {
            const nodeType = n.element.dataset.type;
            if (nodeType === activeType) {
                n.element.classList.add('active');
            } else {
                n.element.classList.remove('active');
            }
        });
    };

    // Serialize the graph logic
    window.getGraphData = function() {
        const topology = {};
        Object.keys(nodes).forEach(id => {
            const type = canonicalRole(nodes[id].element.dataset.type);
            if (!topology[type]) {
                topology[type] = [];
            }
        });
        
        connections.forEach(conn => {
            const fromNode = nodes[conn.from]?.element;
            const toNode = nodes[conn.to]?.element;
            if (fromNode && toNode) {
                const fromType = canonicalRole(fromNode.dataset.type);
                const toType = canonicalRole(toNode.dataset.type);
                if (!topology[fromType].includes(toType)) {
                    topology[fromType].push(toType);
                }
            }
        });
        return topology;
    };
});
