import { expect, test } from "bun:test";

import { slices, visibleGroups } from "./system-metrics";

const token = (key: string, value: string, neg = false) => ({ key, value, neg });

test("a component term keeps the groups that match it and drops the collectors", () => {
	expect(visibleGroups([]).map((g) => g.key)).toEqual(["api", "controller", "build-controller", "clusters"]);
	expect(visibleGroups([token("component", "loco-api")]).map((g) => g.key)).toEqual(["api"]);
	expect(visibleGroups([token("component", "loco-*")]).map((g) => g.key)).toEqual([
		"api",
		"controller",
		"build-controller",
	]);
});

test("each cluster and region named draws its own series", () => {
	expect(slices([], []).map((s) => s.name)).toEqual(["All clusters"]);
	expect(slices([token("cluster", "us-ca-1"), token("cluster", "de-he-1")], []).map((s) => s.attributes)).toEqual([
		{ cluster: "us-ca-1" },
		{ cluster: "de-he-1" },
	]);
	expect(slices([token("cluster", "us-ca-1"), token("region", "us-ca")], [])).toEqual([
		{ name: "us-ca-1 · us-ca", attributes: { cluster: "us-ca-1" }, region: "us-ca" },
	]);
});

test("an excluded region drops its series from the regions named or known", () => {
	const known = ["de-he-1", "us-ca-1", "us-tx-1"];
	expect(slices([token("region", "us-ca-1", true)], known).map((s) => s.region)).toEqual(["de-he-1", "us-tx-1"]);
	expect(
		slices([token("region", "us-ca-1"), token("region", "us-tx-1"), token("region", "us-tx-1", true)], known).map(
			(s) => s.region,
		),
	).toEqual(["us-ca-1"]);
	expect(slices([token("region", "*", true)], known)).toEqual([]);
});
