import { describe, expect, test } from "bun:test";

import { DEFAULT_DOCS_URL, resolveDocsURL } from "./docs-url";

describe("documentation address", () => {
	test("defaults to the public documentation", () => {
		expect(resolveDocsURL(undefined)).toBe(DEFAULT_DOCS_URL);
		expect(resolveDocsURL("")).toBe(DEFAULT_DOCS_URL);
	});

	test("uses the installation's address without a trailing slash", () => {
		expect(resolveDocsURL("https://docs.staging.loco.build/")).toBe("https://docs.staging.loco.build");
	});
});
