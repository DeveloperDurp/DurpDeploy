function olderLogsURL() {
	return `${location.pathname}?before=${this.olderBefore}`;
}

function renderLogLine(element, line = element.textContent) {
	const fragment = document.createDocumentFragment();
	let color = /^\s*[-+]\/[-+]/.test(line) ? 'text-warning'
		: ({ '+': 'text-success', '-': 'text-error', '~': 'text-warning' })[line.trimStart().match(/^([+~-])\s/)?.[1]] || '';
	let bold = false;
	const append = text => {
		if (!text) return;
		const span = document.createElement('span');
		span.className = [color, bold ? 'font-bold' : ''].filter(Boolean).join(' ');
		span.textContent = text;
		fragment.append(span);
	};
	let offset = 0;
	for (const match of line.matchAll(/\x1b\[([0-9;]*)m/g)) {
		append(line.slice(offset, match.index));
		for (const code of match[1].split(';').map(Number)) {
			if (code === 0) { color = ''; bold = false; }
			else if (code === 1) bold = true;
			else if (code === 22) bold = false;
			else if (code === 39) color = '';
			else if ([31, 91].includes(code)) color = 'text-error';
			else if ([32, 92].includes(code)) color = 'text-success';
			else if ([33, 93].includes(code)) color = 'text-warning';
		}
		offset = match.index + match[0].length;
	}
	append(line.slice(offset));
	element.replaceChildren(fragment);
}

export function registerLogComponents(Alpine) {
	const encoder = new TextEncoder();
	const initialRows = element => [...element.querySelectorAll('[data-log-id]')]
		.map(node => ({ id: node.dataset.logId, node,
			bytes: encoder.encode(node.textContent).length }))
		.sort((a, b) => a.id.length - b.id.length || a.id.localeCompare(b.id));
	const trim = component => {
		while (component.retained.length > component.limit ||
			component.retainedBytes > component.byteLimit) {
			const removed = component.retained.shift();
			component.retainedBytes -= removed.bytes;
			if (removed.node) removed.node.remove();
			else removed.panel.live.shift();
			component.olderBefore = component.retained[0]?.id ||
				(BigInt(removed.id) + 1n).toString();
		}
	};
	const initialize = component => {
		component.retained = initialRows(component.$el);
		component.retainedBytes = component.retained.reduce((sum, row) => sum + row.bytes, 0);
		trim(component);
	};
	const retain = (component, row, entry) => {
		row.bytes = encoder.encode(JSON.stringify(entry)).length;
		component.retained.push(row);
		component.retainedBytes += row.bytes;
		trim(component);
	};
	Alpine.data('deploymentStream', ({ url, after, limit, byteLimit, olderBefore }) => ({
		url,
		lastID: after,
		limit,
		byteLimit,
		olderBefore,
		olderLogsURL,
		retained: [],
		retainedBytes: 0,
		source: null,
		started: false,
		destroyed: false,
		init() {
			if (this.started) return;
			this.started = true;
			initialize(this);
			const address = new URL(this.url, location.href);
			address.searchParams.set('after', this.lastID);
			this.source = new EventSource(address);
			this.source.addEventListener('log', event => this.message(event));
			this.source.addEventListener('complete', () => {
				this.source?.close();
				this.source = null;
			});
		},
		message(event) {
			if (this.destroyed) return;
			const entry = JSON.parse(event.data);
			// SSE IDs are exact decimal strings, including beyond Number's range.
			entry.id = event.lastEventId;
			if (BigInt(entry.id) <= BigInt(this.lastID)) return;
			this.lastID = entry.id;
			const node = document.createElement('div');
			node.dataset.logId = entry.id;
			node.textContent = entry.line;
			this.$refs.logs.append(node);
			retain(this, { id: entry.id, node }, entry);
		},
		destroy() {
			this.destroyed = true;
			if (this.source) this.source.close();
			this.source = null;
		},
	}));


	Alpine.data('deploymentStepLogs', ({ url, status, view }) => ({
		renderLogLine,
		renderEntry(element, entry) {
			renderLogLine(element, entry.line);
			if (entry.step.includes(' @ ')) {
				const step = document.createElement('span');
				step.className = 'text-accent';
				step.textContent = `[${entry.step}] `;
				element.prepend(step);
			}
			const timestamp = document.createElement('span');
			timestamp.className = 'text-gray-500';
			timestamp.textContent = `[${entry.display_timestamp}] `;
			element.prepend(timestamp);
		},
		url,
		deploymentStatus: status,
		panels: view.panels,
		lastID: view.lastID,
		limit: view.limit,
		byteLimit: view.byteLimit,
		olderBefore: view.olderBefore,
		olderLogsURL,
		retained: [],
		retainedBytes: 0,
		source: null,
		init() {
			initialize(this);
			if (view.historical) return;
			// Cancellation can set terminal status before the runner writes final logs.
			const address = new URL(this.url, location.href);
			address.searchParams.set('after', this.lastID);
			this.source = new EventSource(address);
			this.source.addEventListener('log', event => this.message(event));
			this.source.addEventListener('complete', event => {
				this.finish(JSON.parse(event.data).status);
				this.source?.close();
				this.source = null;
			});
		},
		terminal() {
			return ['succeeded', 'failed', 'cancelled', 'rejected', 'expired', 'cleanup_unconfirmed'].includes(this.deploymentStatus);
		},
		message(event) {
			const entry = JSON.parse(event.data);
			entry.id = event.lastEventId;
			if (BigInt(entry.id) <= BigInt(this.lastID)) return;
			this.lastID = entry.id;
			let panel = this.panels.find(item => item.index === entry.step_index);
			if (!panel && entry.step) {
				const matches = this.panels.filter(item => item.index >= 0 &&
					(entry.step === item.name || entry.step.startsWith(`${item.name} @ `)));
				if (matches.length === 1) panel = matches[0];
			}
			panel ??= this.panels.find(item => item.index === -1);
			panel.live.push(entry);
			retain(this, { id: entry.id, panel }, entry);
			if (entry.state) {
				panel.state = entry.state;
			} else if (panel.state === 'pending') {
				panel.state = 'unknown';
			}
		},
		stateLabel(state) {
			return ({ waiting: 'Waiting for agents', running: 'Running',
				succeeded: 'Succeeded', failed: 'Failed', cancelled: 'Cancelled',
				not_run: 'Not run', unknown: 'State unavailable' })[state] ?? 'Pending';
		},
		activeStepText() {
			if (['queued', 'publishing_artifact', 'awaiting_artifact_approval'].includes(this.deploymentStatus)) {
				return this.deploymentStatus.replaceAll('_', ' ');
			}
			const active = this.panels.find(panel => panel.index >= 0 && ['running', 'waiting'].includes(panel.state));
			if (active) return `${this.stateLabel(active.state)}: Step ${active.index + 1} — ${active.name}`;
			if (this.terminal()) return `Deployment ${this.deploymentStatus.replaceAll('_', ' ')}`;
			if (this.deploymentStatus === 'running') return 'Current step unavailable';
			return 'Waiting to start';
		},
		statusChanged(event) {
			const target = event.target instanceof Element ? event.target : event.detail?.target;
			if (!(target instanceof Element) || target.id !== 'status-badge') return;
			this.deploymentStatus = target.dataset.deploymentStatus || target.textContent.trim();
			// The stream drains final logs before sending its complete event.
		},
		finish(status) {
			this.deploymentStatus = status;
			for (const panel of this.panels) {
				if (panel.state === 'pending') panel.state = 'unknown';
				if (['running', 'waiting'].includes(panel.state)) {
					panel.state = 'unknown';
				}
			}
		},
		destroy() {
			this.source?.close();
			this.source = null;
		},
	}));
}
