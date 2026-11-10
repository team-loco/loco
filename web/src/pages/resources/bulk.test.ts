import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import {
	deleteText,
	fanOut,
	fanOutSummary,
	listedNames,
	replicasError,
	scaleChange,
	scaleNote,
	scaleTargets,
} from "./bulk";
import type { EnvCell, ResourceRow } from "./table";

const cell: EnvCell = {
	status: ResourceStatus.HEALTHY,
	tag: "1.0.0",
	ready: 1,
	desired: 1,
	regions: ["us-east"],
	note: "",
	deployedMs: 0,
	spec: undefined,
};

function row(id: string, envIds: string[]): ResourceRow {
	return {
		resource: create(ResourceSchema, { id, name: id }),
		cells: new Map(envIds.map((e) => [e, cell])),
		cpuMilli: 0,
		memMi: 0,
		replicas: null,
		address: null,
		image: "",
		createdMs: 0,
	};
}

const blank = { replicas: "", cpu: "", memory: "" };

describe("fanOut", () => {
	test("runs every item and keeps failures with their errors", async () => {
		const boom = new Error("boom");
		const result = await fanOut(["a", "b", "c"], async (x) => {
			await Promise.resolve();
			if (x === "b") throw boom;
		});
		expect(result.done).toEqual(["a", "c"]);
		expect(result.failed).toEqual([{ item: "b", error: boom }]);
		expect(fanOutSummary("Scaled", result)).toBe("Scaled 2 of 3");
	});
});

describe("scale form", () => {
	test("blank fields keep the current values", () => {
		expect(scaleChange(blank)).toBeNull();
		expect(scaleChange({ ...blank, cpu: " 500m " })).toEqual({ cpu: "500m" });
		expect(scaleChange({ replicas: "3", cpu: "", memory: "1Gi" })).toEqual({ replicas: 3, memory: "1Gi" });
	});

	test("rejects replicas that are not a whole number", () => {
		for (const replicas of ["1.5", "-2", "two"]) {
			expect(replicasError({ ...blank, replicas })).toBeDefined();
			expect(scaleChange({ ...blank, replicas, cpu: "1" })).toBeNull();
		}
		expect(replicasError({ ...blank, replicas: "2" })).toBeUndefined();
	});

	test("targets only resources deployed to the environment", () => {
		const { targets, skipped } = scaleTargets([row("a", ["prod"]), row("b", ["stg"])], "prod");
		expect(targets.map((r) => r.resource.id)).toEqual(["a"]);
		expect(skipped).toBe(1);
		expect(scaleNote(1, 1, "production")).toBe(
			"1 run in production, in every region. 1 not deployed there will be skipped.",
		);
		expect(scaleNote(0, 2, "production")).toBe("None of these are deployed to production.");
	});
});

describe("delete text", () => {
	test("names how many selected resources are running", () => {
		expect(deleteText([row("a", ["prod"]), row("b", [])])).toStartWith("1 is running.");
		expect(deleteText([row("a", ["prod"])])).toStartWith("It is running.");
		expect(deleteText([row("a", [])])).toStartWith("Deployments, domains");
	});

	test("lists the first names and counts the rest", () => {
		const names = ["a", "b", "c", "d", "e", "f", "g", "h"];
		expect(listedNames(names)).toEqual({ shown: names.slice(0, 6), more: 2 });
		expect(listedNames(["a"]).more).toBe(0);
	});
});
