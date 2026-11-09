import { expect, test } from "bun:test";

import { checkTarget, domainCheck, domainCoverage, type CheckResult } from "./platform-domain";

const idle: CheckResult = { fetching: false, available: undefined, error: null };

test("checkTarget only queries valid hostnames the resource does not already own", () => {
	expect(checkTarget("web", "onloco.app", [])).toBe("web.onloco.app");
	expect(checkTarget("", "onloco.app", [])).toBeNull();
	expect(checkTarget("web", "", [])).toBeNull();
	expect(checkTarget("-web", "onloco.app", [])).toBeNull();
	expect(checkTarget("web", "onloco.app", ["web.onloco.app"])).toBeNull();
});

test("domainCheck reports each state of the availability check", () => {
	const state = (sub: string, own: string[], result: CheckResult) => domainCheck(sub, "onloco.app", own, result).state;
	expect(state("", [], idle)).toBe("empty");
	expect(state("Web_1", [], idle)).toBe("invalid");
	expect(state("web", ["web.onloco.app"], idle)).toBe("mine");
	expect(state("web", [], idle)).toBe("checking");
	expect(state("web", [], { ...idle, fetching: true, available: true })).toBe("checking");
	expect(state("web", [], { ...idle, available: true })).toBe("available");
	expect(state("web", [], { ...idle, available: false })).toBe("taken");
	expect(state("web", [], { ...idle, error: new Error("down") })).toBe("failed");
	expect(domainCheck("web", "onloco.app", [], { ...idle, available: true }).host).toBe("web.onloco.app");
});

test("domainCoverage splits the resource's regions into served and missing", () => {
	expect(domainCoverage(["us-east-1", "us-west-1"], ["us-east-1", "eu-central-1"])).toEqual({
		serving: ["us-east-1"],
		missing: ["eu-central-1"],
	});
	expect(domainCoverage(["us-east-1"], [])).toEqual({ serving: [], missing: [] });
});
