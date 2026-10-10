import Alpine from 'alpinejs'
import htmx from 'htmx.org'
import { createVariablesPage } from './variables.js'

window.Alpine = Alpine
window.htmx = htmx

function focusFormField(field) {
	// Touch users choose when to open the on-screen keyboard.
	if (!matchMedia('(pointer: coarse)').matches) field?.focus();
}

document.addEventListener('htmx:beforeTransition', (event) => {
	if (event.target.id === 'home-deployments' &&
		matchMedia('(prefers-reduced-motion: reduce)').matches) {
		event.preventDefault();
	}
});

document.addEventListener('htmx:beforeSwap', (event) => {
	if (event.detail.xhr.status === 422 &&
		event.detail.xhr.getResponseHeader('HX-Retarget')) {
		event.detail.shouldSwap = true;
	}
	const target = event.detail.target;
	if (target.hasAttribute('data-artifact-gates') &&
		target.contains(document.activeElement)) {
		event.detail.shouldSwap = false;
	}
});

document.addEventListener('htmx:afterRequest', (event) => {
	const element = event.detail.elt;
	if (element.dataset.terraformReview !== undefined &&
		!event.detail.successful) {
		element.querySelector('.terraform-plan').textContent =
			'Resource changes could not be loaded. Reload this page to retry.';
	}
});

let chartLibrary;
function loadChartLibrary(src) {
	if (!chartLibrary) {
		chartLibrary = new Promise((resolve, reject) => {
			const script = document.createElement('script');
			script.src = src;
			script.onload = () => resolve(window.DashboardChart);
			script.onerror = () => {
				script.remove();
				chartLibrary = null;
				reject(new Error('Chart bundle could not be loaded'));
			};
			document.head.appendChild(script);
		});
	}
	return chartLibrary;
}

Alpine.data('homeCharts', () => {
	// Keep Chart instances outside Alpine's reactive proxy.
	let charts = [], observer, request, Chart;
	let destroyed = false;
	return {
		days: [], totals: [], loading: true, error: false, ready: false, empty: false,
		init() { return this.load(); },
		label(status) {
			return ({ succeeded: 'Succeeded', failed: 'Failed', cancelled: 'Cancelled',
				running: 'Running', pending: 'Pending', pending_approval: 'Awaiting approval',
				queued: 'Queued', publishing_artifact: 'Publishing artifact',
				awaiting_artifact_approval: 'Awaiting artifact approval',
				rejected: 'Rejected', expired: 'Expired',
				cleanup_unconfirmed: 'Cleanup unconfirmed' })[status] || status;
		},
		daySummary(day) {
			return Object.entries(day.counts).map(([status, count]) =>
				`${this.label(status)}: ${count}`).join(', ') || 'No deployments';
		},
		async load() {
			request?.abort();
			request = new AbortController();
			this.loading = this.days.length === 0;
			this.error = false;
			try {
				const response = await fetch('/dashboard/activity', {
					signal: request.signal, headers: { Accept: 'application/json' },
				});
				if (!response.ok) throw new Error('Activity could not be loaded');
				const days = await response.json();
				if (destroyed) return;
				if ((this.ready || this.empty) && JSON.stringify(days) === JSON.stringify(this.days)) return;
				this.days = days;
				const counts = {};
				for (const day of days) for (const [status, count] of Object.entries(day.counts)) {
					counts[status] = (counts[status] || 0) + count;
				}
				this.totals = Object.entries(counts).map(([status, count]) =>
					({ status, count, label: this.label(status) }));
				this.empty = this.totals.length === 0;
				if (this.empty) { this.ready = false; return; }
				Chart = await loadChartLibrary(this.$el.dataset.chartSrc);
				if (destroyed) return;
				this.ready = true;
				await this.$nextTick();
				if (destroyed) return;
				this.render();
				observer ??= new MutationObserver(() => this.render());
				observer.observe(document.documentElement, {
					attributes: true, attributeFilter: ['data-theme'],
				});
			} catch (error) {
				if (!destroyed && error.name !== 'AbortError') this.error = true;
			} finally {
				if (!destroyed) this.loading = false;
			}
		},
		render() {
			if (destroyed || !this.$el.isConnected) return;
			const ink = getComputedStyle(this.$el).color;
			const colors = this.totals.map(item => {
				const token = this.$el.querySelector(`[data-chart-color="${item.status}"]`);
				return token ? getComputedStyle(token).color : ink;
			});
			const options = {
				responsive: true, maintainAspectRatio: false, animation: false,
				color: ink, plugins: { legend: { position: 'bottom',
					labels: { color: ink, boxWidth: 12, boxHeight: 12 } } },
			};
			const configs = [{
				type: 'doughnut', options,
				data: {
					labels: this.totals.map(item => item.label),
					datasets: [{ data: this.totals.map(item => item.count),
						backgroundColor: colors, borderWidth: 0 }],
				},
			}, {
				type: 'bar',
				options: { ...options, scales: {
					x: { stacked: true, grid: { display: false },
						ticks: { color: ink, maxRotation: 0, maxTicksLimit: 7 } },
					y: { stacked: true, beginAtZero: true,
						ticks: { color: ink, precision: 0 }, grid: { color:
							getComputedStyle(this.$refs.gridColor).color } },
				} },
				data: {
					labels: this.days.map(day => day.date.slice(5)),
					datasets: this.totals.map((item, index) => ({
						label: item.label, backgroundColor: colors[index],
						data: this.days.map(day => day.counts[item.status] || 0),
					})),
				},
			}];
			configs.forEach((config, index) => {
				if (charts[index]) {
					charts[index].data = config.data;
					charts[index].options = config.options;
					charts[index].update('none');
				} else {
					const canvas = index === 0 ? this.$refs.outcomes : this.$refs.activity;
					charts[index] = new Chart(canvas, config);
				}
			});
		},
		destroy() {
			destroyed = true;
			request?.abort();
			observer?.disconnect();
			charts.forEach(chart => chart.destroy());
		},
	};
});

// Re-fetch history entries rather than storing protected page content.
htmx.config.historyCacheSize = 0;
htmx.config.historyRestoreAsHxRequest = false;

htmx.onLoad((root) => {
	if (!document.querySelector('meta[name="csrf-token"]')) return;
	const links = [...root.querySelectorAll('a[href]')];
	if (root.matches?.('a[href]')) links.push(root);
	for (const link of links) {
		if (!link.getAttribute('href').startsWith('/') || link.origin !== location.origin ||
			link.hash || link.target || link.hasAttribute('download') ||
			link.closest('[hx-boost], [data-hx-boost], [x-data="backNavigation"]') ||
			link.matches('[hx-get], [hx-post], [hx-put], [hx-patch], [hx-delete]') ||
			/^\/(login|logout|auth|api|static|swagger|healthz|\.well-known)(\/|$)/.test(link.pathname) ||
			link.pathname === '/settings/security/reauth/oidc' ||
			link.pathname.endsWith('/logs.txt')) continue;
		link.setAttribute('hx-boost', 'true');
		link.setAttribute('hx-target', '#page-content');
		link.setAttribute('hx-select', '#page-content');
		link.setAttribute('hx-select-oob', '#app-navbar');
		link.setAttribute('hx-swap', 'outerHTML show:window:top');
		link.setAttribute('hx-sync', 'body:replace');
		htmx.process(link);
	}
});

document.addEventListener('htmx:afterSwap', (event) => {
	if (event.target.id === 'page-content' && event.detail.requestConfig?.boosted) {
		document.getElementById('page-content')?.focus({ preventScroll: true });
	}
});
document.addEventListener('htmx:beforeSwap', (event) => {
	if (event.detail.requestConfig?.boosted && event.detail.xhr.status >= 400) {
		event.preventDefault();
		location.assign(event.detail.xhr.responseURL);
	}
});
document.addEventListener('htmx:historyCacheMissLoadError', (event) => {
	const redirect = event.detail.xhr.getResponseHeader('HX-Redirect');
	if (redirect) location.replace(redirect);
});

Alpine.data('backNavigation', () => ({
	back(event) {
		if (event.defaultPrevented || event.button !== 0 || event.ctrlKey ||
			event.metaKey || event.shiftKey || event.altKey) return;
		const navigation = window.navigation;
		// Navigation entries omit other origins. Only fall back when the
		// browser proves there is no previous entry; otherwise use real history.
		event.preventDefault();
		if (history.length === 1 || (navigation && !navigation.canGoBack &&
			navigation.entries().length === history.length)) {
			location.replace(event.currentTarget.href);
			return;
		}
		history.back();
	},
}));

Alpine.data('toast', () => ({
	visible: false,
	message: '',
	type: 'success',
	timeout: null,
	showToastListener: null,
	makeToastListener: null,
	beforeRequestListener: null,
	afterRequestListener: null,
	requestToasts: new WeakMap(),
	show(msg, type = 'success') {
		if (this.timeout) clearTimeout(this.timeout);
		this.message = String(msg);
		this.type = type;
		this.visible = true;
		this.timeout = setTimeout(() => {
			this.visible = false;
		}, 3000);
	},
	get fullAlertClass() {
		const classMap = {
			'success': 'alert-success',
			'error': 'alert-error',
			'warning': 'alert-warning',
			'info': 'alert-info'
		};
		const alertTypeClass = classMap[this.type] || 'alert-success';
		return `alert shadow-lg ${alertTypeClass}`;
	},
	init() {
		this.showToastListener = (e) => {
			const { message, type } = e.detail;
			this.show(message, type);
		};
		this.makeToastListener = (e) => {
			const { level, message } = e.detail;
			const type = level === 'danger' ? 'error' : level;
			this.show(message, type);
		};
		this.beforeRequestListener = (e) => {
			const trigger = e.detail.elt;
			this.requestToasts.set(e.detail.xhr, {
				success: trigger.getAttribute('data-toast-success'),
				error: trigger.getAttribute('data-toast-error'),
			});
		};
		this.afterRequestListener = (e) => {
			const messages = this.requestToasts.get(e.detail.xhr) || {};
			this.requestToasts.delete(e.detail.xhr);
			const status = e.detail.xhr.status;
			
			if (status >= 200 && status < 400 && messages.success) {
				this.show(messages.success, 'success');
			} else if (status >= 400 && messages.error) {
				this.show(messages.error, 'error');
			}
		};
		window.addEventListener('show-toast', this.showToastListener);
		document.body.addEventListener('makeToast', this.makeToastListener);
		document.body.addEventListener(
			'htmx:beforeRequest',
			this.beforeRequestListener,
		);
		document.body.addEventListener(
			'htmx:afterRequest',
			this.afterRequestListener,
		);
	},
	destroy() {
		if (this.timeout) clearTimeout(this.timeout);
		window.removeEventListener('show-toast', this.showToastListener);
		document.body.removeEventListener('makeToast', this.makeToastListener);
		document.body.removeEventListener(
			'htmx:beforeRequest',
			this.beforeRequestListener,
		);
		document.body.removeEventListener(
			'htmx:afterRequest',
			this.afterRequestListener,
		);
		this.timeout = null;
	}
}));

window.showToast = (msg, type = 'success') => {
	window.dispatchEvent(new CustomEvent('show-toast', { detail: { message: msg, type } }));
};

Alpine.data('navbar', () => ({
	theme: localStorage.getItem('theme') || 'mocha',
	init() {
		document.documentElement.setAttribute('data-theme', this.theme);
		this.$watch('theme', (value) => {
			document.documentElement.setAttribute('data-theme', value);
			localStorage.setItem('theme', value);
		});
	},
	closeMenu(menu) {
		if (!menu.open) return;
		menu.open = false;
		this.$nextTick(() => menu.querySelector(':scope > summary')?.focus());
	},
	closeFocusedMenu(event) {
		const target = event.target instanceof Element ? event.target : null;
		const menu = target?.closest('details[data-focus-menu][open]');
		if (!menu) return;
		event.preventDefault();
		event.stopPropagation();
		this.closeMenu(menu);
	},
	closeOutsideMenu(event) {
		const target = event.target instanceof Element ? event.target : null;
		if (!target) return;
		const menus = [...this.$el.querySelectorAll('details[data-focus-menu][open]')]
			.filter((menu) => !menu.contains(target));
		const menu = menus.find((candidate) =>
			!menus.some((other) => other !== candidate && candidate.contains(other)),
		);
		if (menu) this.closeMenu(menu);
	},
}));

Alpine.data('deploymentForm', ({ releaseID, environmentID }) => ({
	releaseID,
	environmentID,
	submitLabel: 'Deploy',
	environmentAlreadyDeployed: false,
	forceVisible: false,
	releaseChanged() {
		window.location.href = `?release_id=${this.releaseID}`;
	},
	environmentChanged(event) {
		this.environmentID = event.currentTarget.value;
		const option = event.currentTarget.selectedOptions[0];
		this.environmentAlreadyDeployed = option?.dataset.gate === 'already-deployed';
		this.submitLabel = option?.dataset.requiresApproval === 'true'
			? 'Request Approval'
			: 'Deploy';
	},
	forceChanged(event) {
		this.forceVisible = event.currentTarget.checked;
	},
}));

Alpine.data('stepPlacement', (executionTarget = 'local', agentLabel = '', agentExecutionMode = 'host') => ({
	executionTarget,
	agentLabel,
	agentExecutionMode: agentExecutionMode || 'host',
	get usesContainer() {
		return this.executionTarget === 'local' || this.agentExecutionMode === 'container';
	},
}));

Alpine.data('runbookEditor', () => ({
	steps: [],
	nextStepID: 0,
	draft: null,
	editingID: null,
	opener: null,
	init() {
		this.steps = JSON.parse(this.$el.dataset.steps);
		for (const step of this.steps) {
			step.editorID = this.nextStepID++;
			step.network_mode ||= '';
			step.agent_execution_mode ||= 'host';
			step.agent_selectors_text = (step.agent_selectors || []).join(', ');
			step.variable_names_text = (step.variable_names || []).join(', ');
		}
	},
	moveStep(index, offset) {
		const other = index + offset;
		[this.steps[index], this.steps[other]] = [this.steps[other], this.steps[index]];
	},
	removeStep(index) {
		this.steps.splice(index, 1);
	},
	usesContainer(step) {
		return step.execution_target === 'local' || step.agent_execution_mode === 'container';
	},
	changeTarget() {
		this.draft.network_mode = '';
		if (this.draft.execution_target === 'local') this.draft.agent_execution_mode = 'host';
	},
	addStep() {
		this.openStep({
			editorID: this.nextStepID++,
			name: '', script_body: '', interpreter: 'bash', timeout_seconds: 0,
			max_retries: 0, execution_target: 'local', agent_selectors_text: '',
			agent_execution_mode: 'host',
			container_image: '', network_mode: '', variable_names_text: '',
		});
	},
	editStep(step) { this.openStep({ ...step }, step.editorID); },
	openStep(draft, editingID = null) {
		this.draft = draft;
		this.editingID = editingID;
		this.opener = document.activeElement;
		this.$nextTick(() => {
			this.$refs.stepDialog.showModal();
			focusFormField(this.$refs.stepDialog.querySelector('input'));
		});
	},
	saveStep() {
		const step = { ...this.draft };
		if (!this.usesContainer(step)) step.container_image = '';
		if (step.execution_target === 'agent') step.network_mode = '';
		else { step.agent_selectors_text = ''; step.agent_execution_mode = 'host'; }
		if (this.editingID === null) this.steps.push(step);
		else this.steps.splice(this.steps.findIndex(item => item.editorID === this.editingID), 1, step);
		this.$refs.stepDialog.close();
	},
	deleteStep() {
		if (!confirm('Remove this step from the new version?')) return;
		this.removeStep(this.steps.findIndex(step => step.editorID === this.editingID));
		this.$refs.stepDialog.close();
	},
	stepClosed() { if (!this.$refs.stepDialog.open) this.opener?.focus(); },
	destroy() { this.$refs.stepDialog?.close(); },
}));

Alpine.data('projectMenu', () => ({
	closingAnimation: null,
	open() {
		this.closingAnimation?.cancel();
		this.closingAnimation = null;
		this.alignWithNavbar();
		this.$refs.menu.showModal();
	},
	alignWithNavbar() {
		const bottom = document.getElementById('app-navbar').getBoundingClientRect().bottom;
		this.$refs.menu.style.setProperty('--project-menu-top', `${Math.max(0, bottom)}px`);
	},
	close() {
		const dialog = this.$refs.menu;
		if (!dialog.open || this.closingAnimation) return;
		if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
			dialog.close();
			return;
		}
		const panel = dialog.querySelector('.project-menu-panel');
		this.closingAnimation = panel.animate([
			{ transform: getComputedStyle(panel).transform },
			{ transform: 'translateX(100%)' },
		], { duration: 180, easing: 'ease-in' });
		this.closingAnimation.addEventListener('finish', () => {
			this.closingAnimation = null;
			dialog.close();
		}, { once: true });
	},
	closeForLink(event) {
		if (event.target.closest('a')) this.close();
	},
	destroy() {
		this.closingAnimation?.cancel();
		this.$refs.menu.close();
	},
}));

Alpine.data('formDialogHost', () => ({
	opener: null,
	rememberOpener(event) { this.opener = event.currentTarget; },
	afterSettle(event) {
		if (event.detail?.target?.id !== 'project-edit-content') return;
		const title = this.$refs.content.querySelector('.page-header h1')?.textContent.trim();
		if (title) this.$refs.dialog.setAttribute('aria-label', title);
		if (!this.$refs.dialog.open) this.$refs.dialog.showModal();
		focusFormField(this.$refs.content.querySelector('input[name="name"]'));
	},
	beforeSwap(event) {
		if (!this.$refs.dialog?.open || event.detail.xhr.status !== 422) return;
		event.detail.target = this.$refs.content;
		event.detail.swapOverride = 'innerHTML';
		event.detail.shouldSwap = true;
		event.detail.isError = false;
	},
	backClicked(event) {
		if (!event.target.closest('[x-data="backNavigation"]')) return;
		event.preventDefault();
		event.stopImmediatePropagation();
		this.$refs.dialog.close();
	},
	closed() {
		for (const child of this.$refs.content.children) Alpine.destroyTree(child);
		this.$refs.content.replaceChildren();
		(this.opener?.isConnected ? this.opener : this.$refs.editButton)?.focus();
		this.opener = null;
	},
	saved() { this.$refs.editButton?.focus(); },
	destroy() { this.$refs.dialog?.close(); },
}));

Alpine.data('stepFormHost', () => ({
	editOpener: null,
	beforeSwap(event) {
		if (event.detail.xhr.status !== 422 ||
			event.detail.xhr.getResponseHeader('HX-Retarget') !== '#step-edit-content') return;
		event.detail.shouldSwap = true;
		event.detail.isError = false;
	},
	afterSwap(event) {
		if (event.detail?.target?.id === 'step-list' && this.$refs.stepEditDialog?.open) {
			this.$refs.stepEditDialog.close();
		}
	},
	afterSettle(event) {
		if (event.detail?.target?.id !== 'step-edit-content') return;
		const dialog = this.$refs.stepEditDialog;
		if (this.$refs.stepEditContent.querySelector('[data-step-add-form]')) {
			this.editOpener = this.$refs.addStepButton;
		}
		if (!dialog.open) dialog.showModal();
		this.$nextTick(() => focusFormField(this.$refs.stepEditContent.querySelector('input[name="name"]')));
	},
	editClosed(event) {
		if (event.target !== this.$refs.stepEditDialog || this.$refs.stepEditDialog.open) return;
		const content = this.$refs.stepEditContent;
		if (!content.hasChildNodes()) return;
		for (const child of content.children) Alpine.destroyTree(child);
		content.replaceChildren();
		const opener = this.editOpener;
		this.editOpener = null;
		const replacement = [...this.$root.querySelectorAll('[data-step-action="edit"]')]
			.find(button => button.getAttribute('hx-get') === opener?.getAttribute('hx-get') && button.getClientRects().length);
		(opener?.isConnected ? opener : replacement ||
			[...this.$root.querySelectorAll('[data-step-action="edit"]')].find(button => button.getClientRects().length) ||
			this.$refs.addStepButton)?.focus();
	},
	add(event) {
		if (event.detail?.listURL) {
			htmx.ajax('GET', event.detail.listURL, {
				target: '#step-list',
				swap: 'innerHTML',
			});
		}
	},
	// step-form-add, step-form-cancel, and step-form-edit are the host contract.
	handleEvent(event) {
		switch (event.type) {
			case 'step-form-add':
				this.add(event);
				break;
			case 'step-form-cancel':
				if (event.target.closest('#step-edit-dialog')) {
					this.$refs.stepEditDialog.close();
					break;
				}
				break;
			case 'step-form-edit':
				this.editOpener = event.target;
				break;
		}
	},
}));

Alpine.data('stepEditor', () => ({
	script: '',
	diagnostics: [],
	lineNumbers: '1',
	modalOpen: false,
	timer: null,
	request: null,
	destroyed: false,
	init() {
		this.input({ currentTarget: this.$refs.textarea });
	},
	input(event) {
		if (this.destroyed) return;
		this.script = event.currentTarget.value;
		const count = this.script.split('\n').length;
		this.lineNumbers = Array.from(
			{ length: count },
			(_, index) => index + 1,
		).join('\n');
		if (this.timer) clearTimeout(this.timer);
		if (this.request) this.request.abort();
		this.timer = setTimeout(async () => {
			this.timer = null;
			const request = new AbortController();
			this.request = request;
			try {
				const response = await fetch('/api/lint', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ script: this.script }),
					signal: request.signal,
				});
				const data = await response.json();
				if (!this.destroyed && this.request === request) {
					this.diagnostics = data.diagnostics || [];
				}
			} catch (error) {
				if (!this.destroyed && error.name !== 'AbortError') {
					this.diagnostics = [];
				}
			} finally {
				if (this.request === request) this.request = null;
			}
		}, 300);
	},
	scroll(event) {
		const gutter = event.currentTarget === this.$refs.modalTextarea
			? this.$refs.modalGutter
			: this.$refs.gutter;
		gutter.scrollTop = event.currentTarget.scrollTop;
	},
	fullscreen() {
		this.modalOpen = true;
		this.$nextTick(() => {
			this.$refs.modal.showModal();
			focusFormField(this.$refs.modalTextarea);
		});
	},
	destroy() {
		this.destroyed = true;
		if (this.timer) clearTimeout(this.timer);
		if (this.request) this.request.abort();
		this.timer = null;
		this.request = null;
	},
}));

Alpine.data('variablesPage', () => createVariablesPage(focusFormField));

Alpine.data('deploymentStream', ({ url }) => ({
	url,
	source: null,
	started: false,
	destroyed: false,
	init() {
		if (this.started) return;
		this.started = true;
		this.source = new EventSource(this.url);
		this.source.onmessage = (event) => this.message(event);
	},
	message(event) {
		if (this.destroyed) return;
		this.$refs.noLogs?.remove();
		this.$refs.logs.textContent += `${event.data}\n`;
	},
	// 'htmx:afterSwap' supplies the replacement #status-badge to status().
	status(event) {
		const target = event.target instanceof Element
			? event.target
			: event.detail?.target;
		if (!(target instanceof Element) || target.id !== 'status-badge') return;
		const status = target.textContent.trim();
		if (!['succeeded', 'failed', 'cancelled', 'rejected', 'expired'].includes(status)) return;
		if (this.source) this.source.close();
		this.source = null;
	},
	destroy() {
		this.destroyed = true;
		if (this.source) this.source.close();
		this.source = null;
	},
}));

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
	source: null,
	init() {
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
		if (entry.id <= this.lastID) return;
		this.lastID = entry.id;
		let panel = this.panels.find(item => item.index === entry.step_index);
		if (!panel && entry.step) {
			const matches = this.panels.filter(item => item.index >= 0 &&
				(entry.step === item.name || entry.step.startsWith(`${item.name} @ `)));
			if (matches.length === 1) panel = matches[0];
		}
		panel ??= this.panels.find(item => item.index === -1);
		panel.live.push(entry);
		if (entry.state) {
			panel.state = entry.state;
			if (['running', 'waiting', 'failed'].includes(entry.state)) {
				this.$el.querySelector(`[data-step-index="${panel.index}"]`).open = true;
			}
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

function base64URLToBuffer(value) {
	const padded = value.replace(/-/g, '+').replace(/_/g, '/').padEnd(
		value.length + ((4 - (value.length % 4)) % 4),
		'=',
	);
	const binary = window.atob(padded);
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index += 1) {
		bytes[index] = binary.charCodeAt(index);
	}
	return bytes.buffer;
}

function bufferToBase64URL(buffer) {
	const bytes = new Uint8Array(buffer);
	let binary = '';
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return window.btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function publicKeyOptions(options, creation) {
	const publicKey = { ...(options.publicKey || options) };
	publicKey.challenge = base64URLToBuffer(publicKey.challenge);
	if (creation) {
		publicKey.user = { ...publicKey.user, id: base64URLToBuffer(publicKey.user.id) };
		publicKey.excludeCredentials = (publicKey.excludeCredentials || []).map((credential) => ({
			...credential,
			id: base64URLToBuffer(credential.id),
		}));
	} else {
		publicKey.allowCredentials = (publicKey.allowCredentials || []).map((credential) => ({
			...credential,
			id: base64URLToBuffer(credential.id),
		}));
	}
	return { publicKey };
}

function credentialJSON(credential) {
	const response = {
		clientDataJSON: bufferToBase64URL(credential.response.clientDataJSON),
	};
	if (credential.response.attestationObject) {
		response.attestationObject = bufferToBase64URL(credential.response.attestationObject);
	} else {
		response.authenticatorData = bufferToBase64URL(credential.response.authenticatorData);
		response.signature = bufferToBase64URL(credential.response.signature);
		response.userHandle = credential.response.userHandle
			? bufferToBase64URL(credential.response.userHandle)
			: null;
	}
	return {
		id: credential.id,
		rawId: bufferToBase64URL(credential.rawId),
		type: credential.type,
		response,
		clientExtensionResults: credential.getClientExtensionResults(),
		authenticatorAttachment: credential.authenticatorAttachment,
	};
}

function webauthnStatus(element, message, error = false) {
	const status = element.closest('[data-webauthn-container]')?.querySelector('[data-webauthn-status]') ||
		element.parentElement?.querySelector('[data-webauthn-status]');
	if (!status) return;
	status.textContent = message;
	status.setAttribute('role', error ? 'alert' : 'status');
	status.className = error ? 'text-sm text-error' : 'text-sm';
}

function webauthnError(error) {
	if (error?.name === 'AbortError') {
		return 'Passkey request was cancelled. Use an authenticator or recovery code instead.';
	}
	if (error?.name === 'NotAllowedError') {
		return 'Passkey verification was not completed. Use an authenticator or recovery code instead.';
	}
	return 'Passkeys are unavailable. Use an authenticator or recovery code instead.';
}

async function webauthnFetch(url, options) {
	const response = await fetch(url, { credentials: 'same-origin', ...options });
	if (!response.ok) throw new Error('WebAuthn request failed');
	return response;
}

function csrfValue(element) {
	return element.closest('form')?.querySelector('[name="csrf_token"]')?.value ||
		document.querySelector('[name="csrf_token"]')?.value || '';
}

function challengeHeaders(token, csrf) {
	return {
		'Content-Type': 'application/json',
		'X-MFA-Challenge': token,
		'X-MFA-Challenge-CSRF': csrf,
	};
}

async function followWebAuthnResponse(response, element) {
	const contentType = response.headers.get('Content-Type') || '';
	if (!contentType.includes('application/json')) {
		window.location.assign(response.url);
		return;
	}
	const result = await response.json();
	if (result.redirect) {
		window.location.assign(result.redirect);
		return;
	}
	if (result.recovery_codes) {
		const recovery = element.closest('[data-webauthn-container]')?.querySelector('[data-webauthn-recovery]');
		if (recovery) {
			recovery.replaceChildren();
			const heading = document.createElement('p');
			heading.textContent = 'Save these recovery codes. They will not be shown again.';
			const codes = document.createElement('ul');
			codes.className = 'grid grid-cols-2 gap-2 font-mono';
			for (const code of result.recovery_codes) {
				const item = document.createElement('li');
				item.className = 'recovery-code bg-base-300 p-2 rounded';
				item.textContent = code;
				codes.append(item);
			}
			recovery.append(heading, codes);
		}
	}
	webauthnStatus(element, 'Passkey added.', false);
}

async function registerPasskey(event, form) {
	event.preventDefault();
	if (!window.PublicKeyCredential || !navigator.credentials) {
		webauthnStatus(form, webauthnError(), true);
		return;
	}
	const submit = form.querySelector('[type="submit"]');
	submit.disabled = true;
	try {
		const body = new URLSearchParams(new FormData(form));
		const begin = await webauthnFetch(form.action, {
			method: 'POST',
			headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
			body,
		});
		const ceremony = await begin.json();
		const credential = await navigator.credentials.create(publicKeyOptions(ceremony.options, true));
		const finish = await webauthnFetch(form.dataset.webauthnFinish, {
			method: 'POST',
			headers: {
				'X-CSRF-Token': csrfValue(form),
				...challengeHeaders(ceremony.token, ceremony.csrf),
			},
			body: JSON.stringify(credentialJSON(credential)),
		});
		await followWebAuthnResponse(finish, form);
	} catch (error) {
		webauthnStatus(form, webauthnError(error), true);
	} finally {
		submit.disabled = false;
	}
}

async function authenticatePasskey(event, button) {
	event.preventDefault();
	if (!window.PublicKeyCredential || !navigator.credentials) {
		webauthnStatus(button, webauthnError(), true);
		return;
	}
	button.disabled = true;
	try {
		const csrf = csrfValue(button);
		const token = button.dataset.webauthnToken || document.querySelector('[name="challenge_token"]')?.value;
		const challengeCSRF = button.dataset.webauthnChallengeCsrf || document.querySelector('[name="challenge_csrf"]')?.value;
		const headers = { 'X-CSRF-Token': csrf };
		if (token && challengeCSRF) Object.assign(headers, challengeHeaders(token, challengeCSRF));
		const begin = await webauthnFetch(button.dataset.webauthnBegin, {
			method: 'POST',
			headers,
		});
		const options = await begin.json();
		const credential = await navigator.credentials.get(publicKeyOptions(options, false));
		const finish = await webauthnFetch(button.dataset.webauthnFinish, {
			method: 'POST',
			headers: {
				'X-CSRF-Token': csrf,
				...challengeHeaders(token, challengeCSRF),
			},
			body: JSON.stringify(credentialJSON(credential)),
		});
		await followWebAuthnResponse(finish, button);
	} catch (error) {
		webauthnStatus(button, webauthnError(error), true);
	} finally {
		button.disabled = false;
	}
}

document.addEventListener('submit', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const form = target.closest('[data-webauthn-register]');
	if (!(form instanceof HTMLFormElement)) return;
	registerPasskey(event, form);
});
document.addEventListener('click', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const button = target.closest('[data-webauthn-authenticate]');
	if (!(button instanceof HTMLButtonElement)) return;
	authenticatePasskey(event, button);
});

const mfaResetOpeners = new WeakMap();
const passkeyDeleteOpeners = new WeakMap();
const securityDisableOpeners = new WeakMap();

function mfaResetDialog(form) {
	const dialog = document.getElementById(form.dataset.mfaResetDialog);
	return dialog instanceof HTMLDialogElement && typeof dialog.showModal === 'function'
		? dialog
		: null;
}

function passkeyDeleteDialog(form) {
	const dialog = document.getElementById(form.dataset.passkeyDeleteDialog);
	return dialog instanceof HTMLDialogElement && typeof dialog.showModal === 'function'
		? dialog
		: null;
}

function securityDisableDialog(form) {
	const dialog = document.getElementById(form.dataset.securityDisableDialog);
	return dialog instanceof HTMLDialogElement && typeof dialog.showModal === 'function'
		? dialog
		: null;
}

document.addEventListener('submit', (event) => {
	const form = event.target;
	if (!(form instanceof HTMLFormElement)) return;
	if (form.matches('[data-mfa-reset-dialog]')) {
		const dialog = mfaResetDialog(form);
		if (!dialog) return;
		event.preventDefault();
		if (dialog.open) return;
		const opener = form.querySelector('[data-mfa-reset-opener]');
		if (opener instanceof HTMLElement) mfaResetOpeners.set(dialog, opener);
		dialog.showModal();
		return;
	}
	if (!form.matches('[data-mfa-reset-confirmation]')) return;
	if (form.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	form.dataset.submitting = 'true';
	const submit = document.querySelector(`[form="${form.id}"]`);
	if (submit instanceof HTMLButtonElement) submit.disabled = true;
});

document.addEventListener('submit', (event) => {
	const form = event.target;
	if (!(form instanceof HTMLFormElement)) return;
	if (form.matches('[data-security-disable-dialog]')) {
		const dialog = securityDisableDialog(form);
		if (!dialog) return;
		event.preventDefault();
		if (dialog.open) return;
		const opener = form.querySelector('[data-security-disable-opener]');
		if (opener instanceof HTMLElement) securityDisableOpeners.set(dialog, opener);
		dialog.showModal();
		return;
	}
	if (!form.matches('[data-security-disable-confirmation]')) return;
	if (form.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	form.dataset.submitting = 'true';
	const submit = document.querySelector(`[form="${form.id}"]`);
	if (submit instanceof HTMLButtonElement) submit.disabled = true;
});

document.addEventListener('submit', (event) => {
	const form = event.target;
	if (!(form instanceof HTMLFormElement)) return;
	if (form.matches('[data-passkey-delete-dialog]')) {
		const dialog = passkeyDeleteDialog(form);
		if (!dialog) return;
		event.preventDefault();
		if (dialog.open) return;
		const opener = form.querySelector('[data-passkey-delete-opener]');
		if (opener instanceof HTMLElement) passkeyDeleteOpeners.set(dialog, opener);
		dialog.showModal();
		return;
	}
	if (!form.matches('[data-passkey-delete-confirmation]')) return;
	if (form.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	form.dataset.submitting = 'true';
	const submit = document.querySelector(`[form="${form.id}"]`);
	if (submit instanceof HTMLButtonElement) submit.disabled = true;
});

document.addEventListener('close', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-mfa-reset-confirmation-dialog]')) return;
	const opener = mfaResetOpeners.get(dialog);
	mfaResetOpeners.delete(dialog);
	if (opener?.isConnected) opener.focus();
}, true);

document.addEventListener('close', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-security-disable-confirmation-dialog]')) return;
	const opener = securityDisableOpeners.get(dialog);
	securityDisableOpeners.delete(dialog);
	if (opener?.isConnected) opener.focus();
}, true);

document.addEventListener('close', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-passkey-delete-confirmation-dialog]')) return;
	const opener = passkeyDeleteOpeners.get(dialog);
	passkeyDeleteOpeners.delete(dialog);
	if (opener?.isConnected) opener.focus();
}, true);

document.addEventListener('cancel', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const dialog = target.closest('[data-mfa-reset-confirmation-dialog], [data-passkey-delete-confirmation-dialog], [data-security-disable-confirmation-dialog]');
	if (!(dialog instanceof HTMLDialogElement)) return;
	event.preventDefault();
	if (dialog.open) dialog.close();
}, true);

document.addEventListener('click', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-mfa-reset-confirmation-dialog]')) return;
	dialog.close();
});

document.addEventListener('click', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-security-disable-confirmation-dialog]')) return;
	dialog.close();
});

document.addEventListener('click', (event) => {
	const dialog = event.target;
	if (!(dialog instanceof HTMLDialogElement) ||
		!dialog.matches('[data-passkey-delete-confirmation-dialog]')) return;
	dialog.close();
});

document.addEventListener('click', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const button = target.closest('[data-security-disable-confirm]');
	if (!(button instanceof HTMLButtonElement)) return;
	if (button.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	button.dataset.submitting = 'true';
});

document.addEventListener('click', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const button = target.closest('[data-mfa-reset-confirm]');
	if (!(button instanceof HTMLButtonElement)) return;
	if (button.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	button.dataset.submitting = 'true';
});

document.addEventListener('click', (event) => {
	const target = event.target;
	if (!(target instanceof Element)) return;
	const button = target.closest('[data-passkey-delete-confirm]');
	if (!(button instanceof HTMLButtonElement)) return;
	if (button.dataset.submitting === 'true') {
		event.preventDefault();
		return;
	}
	button.dataset.submitting = 'true';
});

Alpine.start()
