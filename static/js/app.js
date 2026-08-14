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

async function registerPasskey(event) {
	event.preventDefault();
	const form = event.currentTarget;
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

async function authenticatePasskey(event) {
	event.preventDefault();
	const button = event.currentTarget;
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

document.querySelectorAll('[data-webauthn-register]').forEach((form) => {
	form.addEventListener('submit', registerPasskey);
});
document.querySelectorAll('[data-webauthn-authenticate]').forEach((button) => {
	button.addEventListener('click', authenticatePasskey);
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

document.querySelectorAll('[data-mfa-reset-confirmation-dialog]').forEach((dialog) => {
	dialog.addEventListener('cancel', (event) => {
		event.preventDefault();
		if (dialog.open) dialog.close();
	});
});

document.querySelectorAll('[data-passkey-delete-confirmation-dialog]').forEach((dialog) => {
	dialog.addEventListener('cancel', (event) => {
		event.preventDefault();
		if (dialog.open) dialog.close();
	});
});

document.querySelectorAll('[data-security-disable-confirmation-dialog]').forEach((dialog) => {
	dialog.addEventListener('cancel', (event) => {
		event.preventDefault();
		if (dialog.open) dialog.close();
	});
});

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
