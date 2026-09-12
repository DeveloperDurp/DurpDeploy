import Alpine from 'alpinejs'
import htmx from 'htmx.org'

window.Alpine = Alpine
window.htmx = htmx

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
	forceVisible: false,
	releaseChanged() {
		window.location.href = `?release_id=${this.releaseID}`;
	},
	environmentChanged(event) {
		this.environmentID = event.currentTarget.value;
		const option = event.currentTarget.selectedOptions[0];
		this.submitLabel = option?.dataset.requiresApproval === 'true'
			? 'Request Approval'
			: 'Deploy';
	},
	forceChanged(event) {
		this.forceVisible = event.currentTarget.checked;
	},
}));

Alpine.data('releaseDeployRow', () => ({
	forceChecked: false,
}));

Alpine.data('stepFormHost', () => ({
	afterRequest(event) {
		const source = event.detail?.elt;
		if (!(source instanceof Element) || !event.detail.successful) return;
		const form = source.closest('form[data-step-add-form]');
		if (!(form instanceof HTMLFormElement)) return;
		this.cancel();
	},
	add(event) {
		if (event.detail?.listURL) {
			htmx.ajax('GET', event.detail.listURL, {
				target: '#step-list',
				swap: 'innerHTML',
			});
		}
	},
	cancel() {
		const host = this.$refs.addStepForm;
		const form = host?.firstElementChild;
		if (!host || !form) return;
		Alpine.destroyTree(form);
		host.replaceChildren();
	},
	// step-form-add, step-form-cancel, and step-form-edit are the host contract.
	handleEvent(event) {
		switch (event.type) {
			case 'step-form-add':
				this.add(event);
				break;
			case 'step-form-cancel':
			case 'step-form-edit':
				this.cancel();
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
			this.$refs.modalTextarea.focus();
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

Alpine.data('variablesPage', () => ({
	afterSwap: null,
	override(event) {
		const button = event.target.closest('[data-override-for]');
		if (!button) return;
		const form = this.$el.querySelector('form');
		if (!form) return;
		const nameInput = form.querySelector('input[name="name"]');
		const environment = form.querySelector('select[name="environment_id"]');
		if (nameInput) nameInput.value = button.dataset.overrideFor;
		if (environment) environment.focus();
		form.scrollIntoView({ behavior: 'smooth', block: 'start' });
	},
	focusAfterSwap(event) {
		const target = event.detail?.target;
		if (!(target instanceof Element) || !this.$el.contains(target)) return;
		const input = target.querySelector('input[name="name"]');
		if (input) input.focus();
	},
	init() {
		this.afterSwap = this.focusAfterSwap.bind(this);
		document.body.addEventListener('htmx:afterSwap', this.afterSwap);
	},
	destroy() {
		document.body.removeEventListener('htmx:afterSwap', this.afterSwap);
		this.afterSwap = null;
	},
}));

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
		const target = event.detail?.elt || event.detail?.target;
		if (!(target instanceof Element) || target.id !== 'status-badge') return;
		const status = target.textContent.trim();
		if (!['succeeded', 'failed', 'cancelled'].includes(status)) return;
		if (this.source) this.source.close();
		this.source = null;
	},
	destroy() {
		this.destroyed = true;
		if (this.source) this.source.close();
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
