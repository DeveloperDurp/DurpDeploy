export function handleBoostedError(event) {
	const { requestConfig, xhr } = event.detail;
	if (!requestConfig?.boosted || xhr.status < 400 ||
		xhr.getResponseHeader('HX-Retarget')) return;
	event.preventDefault();
	if (requestConfig.verb.toLowerCase() === 'get') {
		location.assign(xhr.responseURL);
		return;
	}
	const message = xhr.getResponseHeader('Content-Type')?.startsWith('text/plain')
		? xhr.responseText.trim() : 'The request failed. Please try again.';
	window.showToast(message, 'error');
}
