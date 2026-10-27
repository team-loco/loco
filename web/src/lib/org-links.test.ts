import { describe, expect, test } from "bun:test";

import { expandOrgLink } from "./org-links";

describe("organization links", () => {
	test("replaces every placeholder with the organization ID", () => {
		expect(expandOrgLink("https://support.example.test/orgs/{orgId}/tickets?org={orgId}", "42")).toBe(
			"https://support.example.test/orgs/42/tickets?org=42",
		);
	});

	test("encodes the organization ID", () => {
		expect(expandOrgLink("https://support.example.test/orgs/{orgId}", "a/b?c")).toBe(
			"https://support.example.test/orgs/a%2Fb%3Fc",
		);
	});

	test("leaves a template without a placeholder unchanged", () => {
		expect(expandOrgLink("https://status.example.test", "42")).toBe("https://status.example.test");
	});
});
