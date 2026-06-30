import Alpine from 'alpinejs'
import htmx from 'htmx.org'

window.Alpine = Alpine
window.htmx = htmx

Alpine.data('toast', () => ({
	visible: false,
	message: '',
	type: 'success',
	timeout: null,
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
		console.log('fullAlertClass called, type:', this.type, 'returning:', `alert shadow-lg ${alertTypeClass}`);
		return `alert shadow-lg ${alertTypeClass}`;
	},
	init() {
		const self = this;
		
		window.addEventListener('show-toast', (e) => {
			const { message, type } = e.detail;
			self.show(message, type);
		});
		
		document.body.addEventListener('makeToast', (e) => {
			const { level, message } = e.detail;
			const type = level === 'danger' ? 'error' : level;
			self.show(message, type);
		});
		
		document.body.addEventListener('htmx:afterRequest', (e) => {
			const trigger = e.detail.elt;
			const successMsg = trigger.getAttribute('data-toast-success');
			const errorMsg = trigger.getAttribute('data-toast-error');
			const status = e.detail.xhr.status;
			
			if (status >= 200 && status < 400 && successMsg) {
				self.show(successMsg, 'success');
			} else if (status >= 400 && errorMsg) {
				self.show(errorMsg, 'error');
			}
		});
	}
}));

window.showToast = (msg, type = 'success') => {
	window.dispatchEvent(new CustomEvent('show-toast', { detail: { message: msg, type } }));
};

Alpine.start()
