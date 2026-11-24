export function createVariablesPage(focusFormField) {
	return {
		afterSwap: null,
		focusCreate(summary) {
			const panel = summary.parentElement;
			if (!panel.open) {
				this.$nextTick(() => focusFormField(panel.querySelector('input[name="name"]')));
			}
		},
		override(event) {
			const button = event.target.closest('[data-override-for]');
			if (!button) return;
			const panel = this.$el.querySelector('[data-variable-create]');
			const form = panel?.querySelector('form');
			if (!form) return;
			panel.open = true;
			panel.scrollIntoView({ block: 'start' });
			const nameInput = form.querySelector('input[name="name"]');
			const environment = form.querySelector('select[name="environment_id"]');
			if (nameInput) nameInput.value = button.dataset.overrideFor;
			if (button.dataset.overrideEnvironment !== undefined) {
				environment.value = button.dataset.overrideEnvironment;
				const secret = form.querySelector('input[name="secret"]');
				if (secret.checked !== (button.dataset.overrideSecret === 'true')) secret.click();
				const value = form.querySelector('input[name="value"]');
				value.value = '';
				focusFormField(value);
			} else {
				focusFormField(environment);
			}
		},
		focusAfterSwap(event) {
			const target = event.detail?.target;
			if (!(target instanceof Element) || !this.$el.contains(target)) return;
			const input = target.querySelector('[data-variable-create][open] input[name="name"]');
			focusFormField(input);
		},
		init() {
			this.afterSwap = this.focusAfterSwap.bind(this);
			document.body.addEventListener('htmx:afterSwap', this.afterSwap);
		},
		destroy() {
			document.body.removeEventListener('htmx:afterSwap', this.afterSwap);
			this.afterSwap = null;
		},
	};
}
