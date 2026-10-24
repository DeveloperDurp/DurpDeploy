export function createStepVariables(focusFormField) {
	return {
		url: '',
		query: '', environmentID: '', interpreter: 'bash', target: 'local', mode: 'host',
		catalog: null, loading: false, error: '', announcement: '',
		opener: null, form: null, editor: null, position: null, request: null,
		scrollPositions: [],
		init() { this.url = this.$el.dataset.url; },
		get configured() { return this.matches(this.catalog?.configured || []); },
		get predefined() { return this.matches(this.catalog?.predefined || []); },
		get resultCount() { return this.configured.length + this.predefined.length; },
		matches(items) {
			const query = this.query.trim().toLowerCase();
			return items.filter(item => item.name.toLowerCase().includes(query));
		},
		openViewer(event) {
			this.opener = event.target.closest('button');
			this.form = this.opener?.closest('form[hx-post], form[hx-put]') || null;
			this.editor = this.form?.querySelector('dialog[open] textarea') ||
				this.form?.querySelector('[name=script_body]') || null;
			this.position = this.editor ? {
				start: this.editor.selectionStart, end: this.editor.selectionEnd,
				direction: this.editor.selectionDirection,
				top: this.editor.scrollTop, left: this.editor.scrollLeft,
			} : null;
			this.scrollPositions = [];
			for (let element = this.editor || this.opener; element; element = element.parentElement) {
				this.scrollPositions.push({ element, top: element.scrollTop, left: element.scrollLeft });
			}
			this.query = '';
			this.announcement = '';
			this.$refs.dialog.showModal();
			focusFormField(this.$refs.search);
			void this.load();
		},
		contextQuery() {
			const query = new URLSearchParams();
			if (this.environmentID) query.set('environment_id', this.environmentID);
			if (this.form) {
				for (const field of ['interpreter', 'execution_target', 'agent_execution_mode', 'variable_names']) {
					query.set(field, this.form.elements.namedItem(field)?.value || '');
				}
				if (query.get('execution_target') === 'local') query.set('agent_execution_mode', 'host');
			} else {
				query.set('interpreter', this.interpreter);
				query.set('execution_target', this.target);
				query.set('agent_execution_mode', this.target === 'local' ? 'host' : this.mode);
			}
			return query;
		},
		async load() {
			this.request?.abort();
			const request = new AbortController();
			this.request = request;
			this.loading = true;
			this.error = '';
			try {
				const response = await fetch(`${this.url}?${this.contextQuery()}`, {
					signal: request.signal, headers: { Accept: 'application/json' }, cache: 'no-store',
				});
				if (!response.headers.get('Content-Type')?.includes('application/json')) {
					throw new Error('Your session may have expired. Sign in again to inspect variables.');
				}
				const data = await response.json();
				if (!response.ok) throw new Error(data.error || 'Could not load available variables.');
				if (this.request === request) this.catalog = data;
			} catch (error) {
				if (error.name !== 'AbortError' && this.request === request) {
					this.error = error.message;
					this.catalog = null;
				}
			} finally {
				if (this.request === request) {
					this.loading = false;
					this.request = null;
				}
			}
		},
		async copy(event) {
			const button = event.currentTarget;
			const row = [...this.configured, ...this.predefined].find(item => item.key === button.dataset.key);
			if (!row) return;
			const text = button.dataset.copy === 'name' ? row.name : row.reference;
			if (!text) return;
			try {
				if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
				await navigator.clipboard.writeText(text);
				this.announcement = `Copied ${text}`;
			} catch {
				this.announcement = 'Copy unavailable. Select the name or reference and copy it manually.';
			}
		},
		closeViewer() { this.$refs.dialog.close(); },
		cancelViewer(event) {
			event.preventDefault();
			this.closeViewer();
		},
		closed(event) {
			if (event.target !== this.$refs.dialog || this.$refs.dialog.open) return;
			this.request?.abort();
			this.request = null;
			this.opener?.isConnected && this.opener.focus({ preventScroll: true });
			if (this.editor?.isConnected && this.position) {
				const position = this.position;
				this.editor.setSelectionRange(position.start, position.end, position.direction);
				this.editor.scrollTop = position.top;
				this.editor.scrollLeft = position.left;
			}
			for (const { element, top, left } of this.scrollPositions) {
				if (element.isConnected) { element.scrollTop = top; element.scrollLeft = left; }
			}
			this.scrollPositions = [];
			this.catalog = null;
			this.opener = null;
		},
		destroy() {
			this.request?.abort();
			this.request = null;
			this.$refs.dialog?.close();
		},
	};
}
