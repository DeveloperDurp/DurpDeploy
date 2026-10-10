export function createVariablesPage(focusFormField) {
	return {
		afterSwap: null,
		override(event) {
			const button = event.target.closest('[data-override-for]');
			if (!button) return;
			const form = this.$el.querySelector('form');
			if (!form) return;
			const nameInput = form.querySelector('input[name="name"]');
			const environment = form.querySelector('select[name="environment_id"]');
			if (nameInput) nameInput.value = button.dataset.overrideFor;
			if (button.hasAttribute('data-override-environment')) {
				environment.value = button.dataset.overrideEnvironment;
				const secret = form.querySelector('input[name="secret"]');
				if (secret.checked !== (button.dataset.overrideSecret === 'true')) secret.click();
				const value = form.querySelector('input[name="value"]');
				value.value = '';
				focusFormField(value);
			} else {
				focusFormField(environment);
			}
			form.scrollIntoView({ behavior: 'smooth', block: 'start' });
		},
		focusAfterSwap(event) {
			const target = event.detail?.target;
			if (!(target instanceof Element) || !this.$el.contains(target)) return;
			const input = target.querySelector('input[name="name"]');
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
