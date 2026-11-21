import { expect, test } from "bun:test";

import { domainCheck, domainCoverage, regionalHostname, type CheckResult } from "./platform-domain";

const idle: CheckResult = { fetching: false, available: undefined, error: null };

test("domainCheck reports each state of the availability check", () => {
	const state = (sub: string, own: string[], result: CheckResult) => domainCheck(sub, "onloco.app", own, result).state;
	expect(state("", [], idle)).toBe("empty");
	expect(state("Web_1", [], idle)).toBe("invalid");
	expect(state("x".repeat(63), [], idle)).toBe("checking");
	expect(state("x".repeat(64), [], idle)).toBe("long");
	expect(state(`${"x".repeat(63)}_`, [], idle)).toBe("long");
	expect(state("web", ["web.onloco.app"], idle)).toBe("mine");
	expect(state("web", [], idle)).toBe("checking");
	expect(state("web", [], { ...idle, fetching: true, available: true })).toBe("checking");
	expect(state("web", [], { ...idle, available: true })).toBe("available");
	expect(state("web", [], { ...idle, available: false })).toBe("taken");
	expect(state("web", [], { ...idle, error: new Error("down") })).toBe("failed");
	expect(domainCheck("web", "onloco.app", [], { ...idle, available: true }).host).toBe("web.onloco.app");
});

test("domainCoverage splits the resource's regions into served and missing", () => {
	expect(domainCoverage(["us-va-1", "us-or-1"], ["us-va-1", "de-he-1"])).toEqual({
		serving: ["us-va-1"],
		missing: ["de-he-1"],
	});
	expect(domainCoverage(["us-va-1"], [])).toEqual({ serving: [], missing: [] });
});

test("regionalHostname puts the region between the label and the platform domain", () => {
	expect(regionalHostname("storefront.onloco.build", "onloco.build", "us-ca-1")).toBe(
		"storefront.us-ca-1.onloco.build",
	);
	expect(regionalHostname("a.b.onloco.build", "onloco.build", "us-ca-1")).toBeNull();
	expect(regionalHostname("shop.example.com", "onloco.build", "us-ca-1")).toBeNull();
	expect(regionalHostname("onloco.build", "onloco.build", "us-ca-1")).toBeNull();
});
