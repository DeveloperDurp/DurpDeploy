import { createServer } from "node:net";

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

export function isExplicitlyEmptyResponse(headers) {
	return headers["content-length"] === "0" &&
		headers["transfer-encoding"] === undefined;
}

export async function waitForSettledRename(page, name) {
	const heading = page.getByRole("heading", { name, exact: true });
	await heading.waitFor({ state: "visible" });
	const input = page.getByLabel("Name", { exact: true });
	await input.waitFor({ state: "visible" });
	return {
		headingVisible: await heading.isVisible(),
		inputValue: await input.inputValue(),
	};
}

function reserveTCPAddress(requested) {
	const value = requested ?? "127.0.0.1:0";
	const parsed = new URL(`tcp://${value}`);
	const port = Number(parsed.port);
	if (!parsed.hostname || !Number.isInteger(port) || port < 0 || port > 65535) {
		throw new Error(`invalid address ${value}`);
	}

	return new Promise((resolve, reject) => {
		const server = createServer();
		server.once("error", reject);
		server.listen({ host: parsed.hostname, port, exclusive: true }, () => {
			server.removeListener("error", reject);
			const bound = server.address();
			if (!bound || typeof bound === "string") {
				server.close();
				reject(new Error(`could not resolve bound address ${value}`));
				return;
			}
			const host = bound.family === "IPv6" ? `[${bound.address}]` : bound.address;
			resolve({
				address: `${host}:${bound.port}`,
				release: () => new Promise((releaseResolve, releaseReject) => {
					server.close((error) => error ? releaseReject(error) : releaseResolve());
				}),
			});
		});
	});
}

export async function reserveHarnessAddresses(requestedAddresses, reserve = reserveTCPAddress) {
	const reservations = [];
	try {
		for (const requested of requestedAddresses) {
			reservations.push(await reserve(requested));
		}
	} catch (error) {
		for (const reservation of reservations) await reservation.release();
		const requested = requestedAddresses[reservations.length] ?? "dynamic loopback port";
		const message = error instanceof Error ? error.message : String(error);
		throw new Error(`browser proof address ${requested} is unavailable: ${message}`);
	}

	let released = false;
	return {
		addresses: reservations.map((reservation) => reservation.address),
		release: async () => {
			if (released) return;
			released = true;
			for (const reservation of reservations) await reservation.release();
		},
	};
}
