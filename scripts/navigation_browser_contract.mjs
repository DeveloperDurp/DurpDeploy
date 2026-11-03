import assert from "node:assert/strict";
import { join } from "node:path";
import { writeFile } from "node:fs/promises";

async function loadingFavicon(page, artifactDir, animated = true) {
	await page.waitForFunction(() => document.querySelector('link[rel="icon"]').href.startsWith("data:image/png"));
	const first = await page.locator('link[rel="icon"]').getAttribute("href");
	if (animated) {
		await page.waitForFunction((href) => document.querySelector('link[rel="icon"]').href !== href, first);
	} else {
		await page.waitForTimeout(250);
		assert.equal(await page.locator('link[rel="icon"]').getAttribute("href"), first);
	}
	await writeFile(join(artifactDir, "navigation-favicon.png"), Buffer.from(first.split(",")[1], "base64"));
}

async function restoredFavicon(page) {
	await page.waitForFunction(() => document.querySelector('link[rel="icon"]').getAttribute("href") === "/static/icons/favicon-64.png");
}

// Use an authenticated context against a running instance. No fixtures are deleted.
export async function navigationLoadingContract(context, url, artifactDir) {
	const page = await context.newPage();
	try {
		for (const [width, theme, path = "/projects"] of [[1280, "mocha"], [375, "light"], [1280, "mocha", "/admin/audit"]]) {
			await page.setViewportSize({ width, height: 900 });
			await page.goto(`${url}/`);
			await page.evaluate((value) => {
				localStorage.setItem("theme", value);
				document.documentElement.dataset.theme = value;
			}, theme);
			const indicator = page.locator("#navigation-loading");
			assert.equal(await indicator.isVisible(), false);
			const contentTop = (await page.locator("#page-content").boundingBox()).y;
			let release;
			const gate = new Promise((resolve) => { release = resolve; });
			await page.route(`**${path}`, async (route) => {
				await gate;
				await route.continue();
			});
			try {
				if (width === 375) await page.locator("[data-mobile-nav] > summary").click();
				if (path === "/admin/audit") await page.locator('#app-navbar summary:visible').filter({ hasText: /^Admin$/ }).click();
				const link = page.locator(`#app-navbar a[href="${path}"]:visible`);
				await link.focus();
				await page.keyboard.press("Enter");
				await indicator.waitFor({ state: "visible" });
				assert.equal(await page.getByRole("progressbar", { name: "Loading page", exact: true }).count(), 1);
				assert.equal(await page.locator('#app-navbar details[open]').count(), 0);
				await loadingFavicon(page, artifactDir);
				assert.equal(new URL(page.url()).pathname, "/");
				assert.match(await indicator.innerText(), /Loading page/);
				const box = await indicator.boundingBox();
				const navbar = await page.locator("#app-navbar").boundingBox();
				assert.ok(Math.abs(box.y - navbar.y - navbar.height) <= 1);
				assert.equal(box.width, navbar.width);
				assert.equal(box.height, 4);
				assert.equal((await page.locator("#page-content").boundingBox()).y, contentTop);
				assert.equal(await indicator.evaluate((element) => getComputedStyle(element, '::before').animationName), 'navigation-progress');
				await page.screenshot({ path: join(artifactDir, `navigation-loading-${theme}-${width}.png`) });
			} finally {
				release();
			}
			await page.waitForURL(`${url}${path}`);
			await indicator.waitFor({ state: "hidden" });
			await restoredFavicon(page);
			await page.unroute(`**${path}`);
		}

		await page.setViewportSize({ width: 1280, height: 900 });
		await page.evaluate(() => new Promise((resolve) => {
			document.addEventListener("htmx:historyRestore", () => resolve(), { once: true });
			history.back();
		}));
		assert.equal(new URL(page.url()).pathname, "/");
		await page.emulateMedia({ reducedMotion: "reduce" });
		let finish;
		const failedRequest = new Promise((resolve) => { finish = resolve; });
		await page.route("**/projects", async (route) => {
			await failedRequest;
			await route.abort("failed");
		});
		try {
			await page.locator('#app-navbar a[href="/projects"]:visible').click();
			await page.locator("#navigation-loading").waitFor({ state: "visible" });
			await loadingFavicon(page, artifactDir, false);
			assert.equal(await page.locator("#navigation-loading").evaluate(
				(element) => getComputedStyle(element, '::before').animationName), "none");
		} finally {
			finish();
		}
		await page.locator("#navigation-loading").waitFor({ state: "hidden" });
		await restoredFavicon(page);
		assert.equal(new URL(page.url()).pathname, "/");
		await page.unroute("**/projects");
		await page.locator('#app-navbar a[href="/environments"]:visible').click();
		await page.waitForURL(`${url}/environments`);
		await page.locator("#navigation-loading").waitFor({ state: "hidden" });

		const projectName = `Navigation loading ${Date.now()}`;
		const project = await page.request.post(`${url}/projects`, { form: {
			name: projectName,
			csrf_token: await page.locator('meta[name="csrf-token"]').getAttribute("content"),
		} });
		assert.ok(project.ok());
		await page.goto(`${url}/projects`);
		const projectURL = new URL(await page.locator('a[href^="/projects/"]').filter({ hasText: projectName }).first().getAttribute("href"), url).href;
		assert.match(projectURL, /\/projects\/\d+$/);
		await page.goto(projectURL);
		let releaseSection;
		const sectionGate = new Promise((resolve) => { releaseSection = resolve; });
		await page.route(`${projectURL}/variables`, async (route) => {
			await sectionGate;
			await route.continue();
		});
		try {
			await page.locator('button', { hasText: "Project menu" }).click();
			await page.locator('#project-menu-dialog a', { hasText: "Variables" }).click();
			await page.locator("#navigation-loading").waitFor({ state: "visible" });
			await loadingFavicon(page, artifactDir, false);
		} finally {
			releaseSection();
		}
		await page.waitForURL(`${projectURL}/variables`);
		await page.locator("#navigation-loading").waitFor({ state: "hidden" });
		await restoredFavicon(page);
		console.log("PASS: navigation indicator and favicon, desktop/mobile, themes, reduced motion, failure, retry and project sections");
	} finally {
		await page.close();
	}
}
