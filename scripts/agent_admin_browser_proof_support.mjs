const resourceFailurePattern =
	/^Failed to load resource: the server responded with a status of (\d{3})(?: \([^)]*\))?$/;
const instrumentedPages = new WeakSet();

export function attachPageDiagnostics(page, errors) {
	if (instrumentedPages.has(page)) return;
	instrumentedPages.add(page);
	page.on("console", (message) => {
		if (message.type() === "error") {
			errors.push({ text: message.text(), url: message.location().url });
		}
	});
	page.on("pageerror", (error) => errors.push({ text: error.message, url: "" }));
}

export function findUnexpectedConsoleErrors(errors, expectedFailures, baseURL) {
	const remaining = expectedFailures.map((failure) => ({
		...failure,
		url: new URL(failure.path, baseURL).href,
	}));

	return errors.filter((error) => {
		const match = resourceFailurePattern.exec(error.text);
		if (!match || !error.url) return true;

		const status = Number(match[1]);
		const expectedIndex = remaining.findIndex((failure) =>
			failure.status === status && failure.url === error.url,
		);
		if (expectedIndex === -1) return true;
		remaining.splice(expectedIndex, 1);
		return false;
	});
}

export function consoleErrorTexts(errors) {
	return errors.map((error) => error.text);
}
