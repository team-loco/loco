import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { RegionInfoSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import {
	addDraft,
	DRAFT_RANK,
	draftIsPublic,
	draftRegions,
	draftSizing,
	draftsKey,
	editDraft,
	parseDrafts,
	removeDraft,
	type Draft,
} from "./drafts";
import { statusRank } from "./resource-status";

function draft(id: string, name = id): Draft {
	return { id, name, image: `ghcr.io/acme/${name}:1`, createdMs: 1, edits: {} };
}

describe("draftsKey", () => {
	test("keeps organizations, workspaces and environments apart", () => {
		const keys = new Set([
			draftsKey("o1", "w1", "e1"),
			draftsKey("o2", "w1", "e1"),
			draftsKey("o1", "w2", "e1"),
			draftsKey("o1", "w1", "e2"),
		]);
		expect(keys.size).toBe(4);
	});
});

describe("parseDrafts", () => {
	test("reads back what was stored", () => {
		const drafts = [draft("a"), { ...draft("b"), edits: { replicas: 3, regions: ["us-east-1"] } }];
		expect(parseDrafts(JSON.parse(JSON.stringify(drafts)))).toEqual(drafts);
	});

	test("returns no drafts for a missing or foreign value", () => {
		expect(parseDrafts(null)).toEqual([]);
		expect(parseDrafts({ id: "a" })).toEqual([]);
	});

	test("drops entries that are not drafts", () => {
		expect(parseDrafts([draft("a"), { id: "b" }, null, 3])).toEqual([draft("a")]);
	});
});

describe("draft changes", () => {
	test("add appends, edit merges into one draft, remove drops one", () => {
		const added = addDraft([draft("a")], draft("b"));
		expect(added.map((d) => d.id)).toEqual(["a", "b"]);
		const edited = editDraft(editDraft(added, "b", { cpu: "1" }), "b", { memory: "2Gi" });
		expect(edited[0]).toEqual(draft("a"));
		expect(edited[1]?.edits).toEqual({ cpu: "1", memory: "2Gi" });
		expect(removeDraft(edited, "a").map((d) => d.id)).toEqual(["b"]);
	});
});

describe("draft placement", () => {
	const regions = [
		create(RegionInfoSchema, { region: "us-east-1", isDefault: true }),
		create(RegionInfoSchema, { region: "eu-west-1", isDefault: false }),
	];

	test("regions default to the environment's default regions until edited", () => {
		expect(draftRegions(draft("a"), regions)).toEqual(["us-east-1"]);
		expect(draftRegions({ ...draft("a"), edits: { regions: ["eu-west-1"] } }, regions)).toEqual(["eu-west-1"]);
		expect(draftRegions({ ...draft("a"), edits: { regions: [] } }, regions)).toEqual([]);
	});

	test("sizing comes from the draft's edits, then the service defaults", () => {
		const defaults = { cpu: "500m", memory: "1Gi", minReplicas: 1, cpuTarget: 65 };
		expect(draftSizing(draft("a"), defaults)).toEqual({
			cpu: "500m",
			memory: "1Gi",
			replicas: 1,
			maxReplicas: 1,
			cpuTarget: 65,
		});
		expect(draftSizing({ ...draft("a"), edits: { cpu: "2", replicas: 3 } }, defaults)).toEqual({
			cpu: "2",
			memory: "1Gi",
			replicas: 3,
			maxReplicas: 3,
			cpuTarget: 65,
		});
	});

	test("a draft is public until switched to internal", () => {
		expect(draftIsPublic(draft("a"))).toBe(true);
		expect(draftIsPublic({ ...draft("a"), edits: { isPublic: false } })).toBe(false);
	});
});

test("drafts rank after deploying and before healthy", () => {
	expect(DRAFT_RANK).toBeGreaterThan(statusRank(ResourceStatus.DEPLOYING));
	expect(DRAFT_RANK).toBeLessThan(statusRank(ResourceStatus.HEALTHY));
});

test("draft persistence keeps secret names without secret values", () => {
	const [edited] = editDraft([draft("a")], "a", {
		vars: [
			{ id: "1", key: "API_KEY", value: "private-test-value", secret: true },
			{ id: "2", key: "LOG_LEVEL", value: "info" },
		],
	});
	expect(edited?.edits.vars).toEqual([
		{ id: "1", key: "API_KEY", value: "", secret: true },
		{ id: "2", key: "LOG_LEVEL", value: "info" },
	]);
});
