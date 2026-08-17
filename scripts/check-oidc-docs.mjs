#!/usr/bin/env node
import {
	mkdtempSync,
	mkdirSync,
	readFileSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = process.env.OIDC_DOCS_ROOT
	? resolve(process.env.OIDC_DOCS_ROOT)
	: resolve(new URL("..", import.meta.url).pathname);
const files = [
	"README.md",
	"docs/deploy.md",
	"docs/security.md",
	"docs/roles.md",
	"docs/attack-drill.md",
	"docs/authentik-oidc.md",
];
const variables = [
	"DURPDEPLOY_URL",
	"DURPDEPLOY_OIDC_ISSUER",
	"DURPDEPLOY_OIDC_CLIENT_ID",
	"DURPDEPLOY_OIDC_CLIENT_SECRET",
	"DURPDEPLOY_OIDC_ADMIN_GROUP",
	"DURPDEPLOY_OIDC_DEPLOYER_GROUP",
	"DURPDEPLOY_OIDC_VIEWER_GROUP",
	"DURPDEPLOY_OIDC_DISPLAY_NAME",
	"DURPDEPLOY_OIDC_GROUP_CLAIM",
	"DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED",
];
const expectedVariableCount = 10;

function fail(message) {
	throw new Error(`OIDC documentation contract: ${message}`);
}

function readDocuments(documentRoot) {
	return files.map((file) => {
		const path = join(documentRoot, file);
		let text;
		try {
			text = readFileSync(path, "utf8");
		} catch (error) {
			if (error instanceof Error && "code" in error && error.code === "ENOENT") {
				fail(`missing ${file}`);
			}
			throw error;
		}
		return { file, text };
	});
}

function assertSecretPlaceholders(documents) {
	const assignment = /DURPDEPLOY_OIDC_CLIENT_SECRET\s*[:=]\s*([^\s#]+)/g;
	const placeholders = /^(?:<[^>]+>|\$\{[^}]+\}|REDACTED_TEST_VALUE)$/;
	for (const { file, text } of documents) {
		for (const match of text.matchAll(assignment)) {
			if (!placeholders.test(match[1])) {
				fail(`real-looking client secret assignment in ${file}`);
			}
		}
	}
}

function assertNoFalseClaims(documents) {
	const forbidden = [
		/\bOIDC\b[^\n.]{0,100}\b(?:logs out|logs the user out)\b[^\n.]{0,80}\b(?:provider|IdP)\b/i,
		/\bupstream logout\b/i,
		/\bOIDC\b[^\n.]{0,80}\b(?:authenticates|protects|issues)\b[^\n.]{0,40}\bAPI tokens?\b/i,
		/\bOIDC login\b[^\n.]{0,80}\b(?:provides|asserts|completes)\b[^\n.]{0,30}\blocal MFA\b/i,
	];
	for (const { file, text } of documents) {
		for (const line of text.split("\n")) {
			const instantDeprovisioning =
				/\b(?:instant|immediate) deprovisioning\b/i.test(line) &&
				!(/\b(?:not|no)\s+(?:instant|immediate)\b/i.test(line));
			const upstreamLogoutAllowed = /does not configure an upstream logout/i.test(line) ||
				/no upstream logout/i.test(line) ||
				/there is no upstream logout/i.test(line);
			if (instantDeprovisioning || forbidden.some((pattern) => pattern.test(line)) && !upstreamLogoutAllowed) {
				fail(`forbidden OIDC claim in ${file}: ${line.trim()}`);
			}
		}
	}
}

function checkDocuments(documentRoot) {
	const documents = readDocuments(documentRoot);
	const corpus = documents.map(({ text }) => text).join("\n");
	if (variables.length !== expectedVariableCount) {
		fail(`expected ${expectedVariableCount} OIDC variables, found ${variables.length}`);
	}
	for (const { file, text } of documents) {
		if (!text.includes("DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED")) {
			fail(`missing DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED in ${file}`);
		}
	}
	for (const [pattern, description] of [
		[/\/login\/oidc\/callback/g, "exact callback path"],
		[/verified email[^\n.]{0,100}(?:links|match)/i, "verified-email linking"],
		[/logout[^\n.]{0,100}local only/i, "local-only logout"],
		[/tokens?[\s\S]{0,150}(?:not|never|does not) (?:persist|store)/i, "no token persistence"],
		[/next OIDC login/i, "next-SSO-login deprovision timing"],
		[/unset[\s\S]{0,180}literal JSON boolean `email_verified: true`/i, "strict default email verification"],
		[/DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED=false/i, "explicit lowercase false setting"],
		[/present literal JSON boolean[\s\S]{0,100}`email_verified: true`[\s\S]{0,100}`email_verified: false`/i, "explicit false accepts both boolean values"],
		[/signature, issuer, audience, and nonce verification/i, "normal ID-token verification"],
		[/missing, null, string, and numeric claims?[^.\n]{0,30}rejected/i, "fail-closed email verification"],
		[/weakens identity assurance[\s\S]{0,120}Authentik[\s\S]{0,80}address ownership/i, "email-assurance warning"],
	]) {
		if (!pattern.test(corpus)) fail(`missing ${description}`);
	}
	assertSecretPlaceholders(documents);
	assertNoFalseClaims(documents);
}

function runFixtureTests() {
	for (const value of ["<secret>", "${SECRET_REF}", "REDACTED_TEST_VALUE"]) {
		const testRoot = mkdtempSync(join(tmpdir(), "oidc-docs-placeholder-"));
		try {
			for (const file of files) {
				const target = join(testRoot, file);
				const directory = target.slice(0, target.lastIndexOf("/"));
				mkdirSync(directory, { recursive: true });
				writeFileSync(target, readFileSync(join(root, file), "utf8"));
			}
			writeFileSync(join(testRoot, "README.md"),
				`${readFileSync(join(testRoot, "README.md"), "utf8")}\nDURPDEPLOY_OIDC_CLIENT_SECRET=${value}\n`);
			checkDocuments(testRoot);
		} finally {
			rmSync(testRoot, { recursive: true, force: true });
		}
	}

	const badRoot = mkdtempSync(join(tmpdir(), "oidc-docs-secret-"));
	try {
		for (const file of files) {
			const target = join(badRoot, file);
			const directory = target.slice(0, target.lastIndexOf("/"));
			mkdirSync(directory, { recursive: true });
			writeFileSync(target, readFileSync(join(root, file), "utf8"));
		}
		writeFileSync(join(badRoot, "README.md"),
			`${readFileSync(join(badRoot, "README.md"), "utf8")}\nDURPDEPLOY_OIDC_CLIENT_SECRET=actual-secret-value\n`);
		let rejected = false;
		try {
			checkDocuments(badRoot);
		} catch (error) {
			rejected = error instanceof Error && error.message.includes("real-looking");
		}
		if (!rejected) fail("failure fixture accepted a real-looking client secret");
	} finally {
		rmSync(badRoot, { recursive: true, force: true });
	}
}

try {
	checkDocuments(root);
	runFixtureTests();
		console.log(`OIDC documentation contract: OK (${files.length} documents, ${expectedVariableCount} OIDC variables)`);
} catch (error) {
	console.error(error instanceof Error ? error.message : "unexpected failure");
	process.exitCode = 1;
}
