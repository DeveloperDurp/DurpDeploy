export function initNavigationFavicon() {
	let indicator = document.getElementById('navigation-loading');
	const icon = document.querySelector('link[rel="icon"]');
	if (!indicator || !icon) return;
	const originalHref = icon.getAttribute('href');
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = 64;
	const context = canvas.getContext('2d');
	if (!context) return;
	const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
	let timer;
	function update() {
		clearInterval(timer);
		if (!indicator?.classList.contains('htmx-request')) {
			icon.setAttribute('href', originalHref);
			return;
		}
		let angle = -Math.PI / 2;
		const draw = () => {
			context.clearRect(0, 0, 64, 64);
			context.lineWidth = 8;
			context.lineCap = 'round';
			context.beginPath();
			context.strokeStyle = '#7f849c';
			context.arc(32, 32, 24, 0, Math.PI * 2);
			context.stroke();
			context.beginPath();
			context.strokeStyle = '#8bb4ff';
			context.arc(32, 32, 24, angle, angle + Math.PI * 1.4);
			context.stroke();
			icon.setAttribute('href', canvas.toDataURL('image/png'));
			angle += Math.PI / 6;
		};
		draw();
		if (!reducedMotion.matches) timer = setInterval(draw, 100);
	}
	// HTMX counts outstanding requests on this shared indicator, including aborts.
	const observer = new MutationObserver(update);
	function observeIndicator() {
		observer.disconnect();
		indicator = document.getElementById('navigation-loading');
		if (indicator) observer.observe(indicator, { attributes: true, attributeFilter: ['class'] });
		update();
	}
	// History restores replace the body, including the shared indicator.
	document.addEventListener('htmx:historyRestore', observeIndicator);
	reducedMotion.addEventListener('change', update);
	window.addEventListener('pagehide', () => {
		clearInterval(timer);
		icon.setAttribute('href', originalHref);
	});
	window.addEventListener('pageshow', update);
	observeIndicator();
}

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
	window.showToast(message, 'error', event.detail.elt);
}
