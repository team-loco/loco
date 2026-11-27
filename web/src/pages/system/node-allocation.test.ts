import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { GetNodeAllocationsResponseSchema } from "@gen/loco/cluster/v1/cluster_pb";
import { allocationRects, allocationTiles } from "./node-allocation";

function snapshot() {
	return create(GetNodeAllocationsResponseSchema, {
		sample: { id: "source:1000", stale: false },
		node: {
			name: "node-a",
			cpuMillicoresAllocatable: 100000n,
			cpuMillicoresRequested: 50000n,
			memoryBytesAllocatable: 100000n,
			memoryBytesRequested: 50000n,
			resourceCount: 100000,
		},
		top: Array.from({ length: 12 }, (_, index) => ({
			id: `workspace-${index}`,
			name: `Workspace ${index}`,
			kind: "workspace",
			cpuMillicores: 1000n,
			memoryBytes: 1000n,
		})),
		remainder: { id: "other", name: "Other", kind: "other", cpuMillicores: 35000n, memoryBytes: 35000n },
		system: { id: "system", name: "System", kind: "system", cpuMillicores: 3000n, memoryBytes: 3000n },
	});
}

describe("node reservation map", () => {
	test("keeps the full server remainder and explicit system reservations in a 15-tile proportional map", () => {
		const data = snapshot();
		const tiles = allocationTiles(data);
		expect(tiles).toHaveLength(15);
		expect(tiles.find((tile) => tile.consumer?.kind === "other")?.value).toBe(35000);
		expect(tiles.find((tile) => tile.consumer?.kind === "system")?.value).toBe(3000);
		expect(tiles.find((tile) => tile.available)?.value).toBe(50000);
		const rects = allocationRects(tiles);
		const total = tiles.reduce((sum, tile) => sum + tile.value, 0);
		for (const rect of rects) {
			expect(rect.width * rect.height).toBeCloseTo((rect.value / total) * 10000, 7);
			expect(rect.x + rect.width).toBeLessThanOrEqual(100.00000001);
			expect(rect.y + rect.height).toBeLessThanOrEqual(100.00000001);
		}
	});
	test("keeps opaque workspace names distinct from system, remainder and available tiles", () => {
		const data = snapshot();
		for (const [index, id] of ["system", "other", "available"].entries()) data.top[index]!.id = id;
		const tiles = allocationTiles(data);
		expect(new Set(tiles.map((tile) => tile.id)).size).toBe(tiles.length);
		expect(tiles.filter((tile) => tile.consumer?.kind === "workspace")).toHaveLength(12);
	});
	test("does not infer available capacity from missing or stale sources", () => {
		for (const state of ["missing", "stale-requests", "stale-capacity"]) {
			const data = snapshot();
			if (state === "missing") data.node!.cpuMillicoresAllocatable = undefined;
			if (state === "stale-requests") data.sample!.stale = true;
			if (state === "stale-capacity") data.node!.capacityStale = true;
			expect(allocationTiles(data).some((tile) => tile.available)).toBe(false);
		}
	});
	test("keeps oversubscribed requests instead of hiding them in negative available space", () => {
		const data = snapshot();
		data.node!.cpuMillicoresAllocatable = 40000n;
		const tiles = allocationTiles(data);
		expect(tiles.reduce((sum, tile) => sum + tile.value, 0)).toBe(50000);
		expect(tiles.some((tile) => tile.available)).toBe(false);
	});
	test("uses response-owned metric and workspace scope for the selected map", () => {
		const data = snapshot();
		data.memory = true;
		data.workspaceId = "workspace-0";
		data.system = undefined;
		data.remainder = undefined;
		data.top = [{ ...data.top[0]!, kind: "resource", cpuMillicores: 9000n, memoryBytes: 2000n }];
		expect(allocationTiles(data).map((tile) => tile.value)).toEqual([2000]);
	});
});
