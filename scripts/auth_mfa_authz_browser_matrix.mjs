import { check } from "./mfa_browser_support.mjs";
import { startBrowserFixture } from "./mfa_browser_fixture.mjs";

const root = new URL("..", import.meta.url).pathname;
const users = {
	admin: { email: "admin@mfa.test", password: "admin-password-1234" },
	deployer: { email: "deployer@mfa.test", name: "Deployer", password: "deployer-password-1234", role: "deployer" },
	viewer: { email: "viewer@mfa.test", name: "Viewer", password: "viewer-password-1234", role: "viewer" },
};

async function run() {
	const fixture = await startBrowserFixture(root);
	try {
		const { viewer } = await fixture.namedUsers(users);
		const response = await viewer.page.evaluate(async () => {
			const csrf = document.querySelector('meta[name="csrf-token"]')?.content ?? "";
			const result = await fetch("/projects", {
				body: new URLSearchParams({ csrf_token: csrf, name: "viewer-browser-write" }),
				headers: { "HX-Request": "true", "X-CSRF-Token": csrf },
				method: "POST",
			});
			return { body: await result.text(), status: result.status, trigger: result.headers.get("HX-Trigger") };
		});
		check(response.status === 200, "viewer HTMX write did not return 200");
		check(response.body === "", "viewer HTMX write returned a swap body");
		check(response.trigger?.includes("makeToast"), "viewer HTMX write omitted toast trigger");
		check(response.trigger?.includes("Viewers cannot perform write operations"), "viewer toast message changed");

		const forbidden = await viewer.page.evaluate(async () => {
			const result = await fetch("/projects", { body: "name=viewer-form", method: "POST" });
			return { body: await result.text(), contentType: result.headers.get("Content-Type"), status: result.status };
		});
		check(forbidden.status === 403, "viewer form write did not return 403");
		check(forbidden.contentType?.includes("text/html"), "viewer form write was not HTML");
		check(forbidden.body.includes("<h1>Forbidden</h1>"), "viewer form write was not styled");
		check(forbidden.body.includes("javascript:history.back()"), "viewer form write omitted back link");

		await viewer.page.goto(`${fixture.app.url}/projects/new`);
		check(await viewer.page.getByText("Viewers cannot create a project.").isVisible(), "viewer form guard missing");
		check(await viewer.page.getByRole("link", { name: "Go back" }).isVisible(), "viewer form guard back link missing");
		check((await viewer.page.locator('a[href="/projects/new"]').count()) === 0, "viewer sees project creation control");
		console.log("Auth/MFA browser authorization matrix: OK (no secret output)");
	} finally {
		await fixture.close();
	}
}

run().catch((error) => {
	console.error(`Auth/MFA browser authorization matrix failed: ${error instanceof Error ? error.message : "unexpected failure"}`);
	process.exitCode = 1;
});
