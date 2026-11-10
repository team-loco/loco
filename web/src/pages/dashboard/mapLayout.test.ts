import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { RegionInfoSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { MAP_MIN_GAP, MAP_NODE_H, MAP_NODE_W, MAP_PUBLIC_LIMIT, mapLayout } from "./mapLayout";
import { draftItems, itemName } from "./resourceRows";
import { draft, row } from "./testRows";

const WIDE = 1200;
const PHONE = 350;

function middle(y: number | undefined): number {
	return (y ?? 0) + MAP_NODE_H / 2;
}

describe("mapLayout", () => {
	test("columns run left to right: Internet, gateways, public resources, internal resources", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("queue")], ["us-east-1"], WIDE);
		const gateway = layout.gateways[0];
		const web = layout.publicNodes[0];
		const queue = layout.internalNodes[0];
		expect(layout.internet?.x).toBe(0);
		expect(gateway?.x).toBeGreaterThan(layout.internet?.x ?? 0);
		expect(web?.x).toBeGreaterThan(gateway?.x ?? 0);
		expect(queue?.x).toBeGreaterThan(web?.x ?? 0);
		expect((queue?.x ?? 0) + MAP_NODE_W).toBeCloseTo(WIDE);
		expect(layout.width).toBe(WIDE);
	});

	test("columns too wide for the space keep a minimum gap and widen the map instead", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("queue")], ["us-east-1"], PHONE);
		expect(layout.width).toBe(4 * MAP_NODE_W + 3 * MAP_MIN_GAP);
		expect((layout.gateways[0]?.x ?? 0) - MAP_NODE_W).toBe(MAP_MIN_GAP);
	});

	test("each column's rows stack downwards and are centred on the tallest column", () => {
		const layout = mapLayout(
			[
				row("api", { domain: "api.example.dev" }),
				row("web", { domain: "web.example.dev" }),
				row("www", { domain: "www.example.dev" }),
			],
			["us-east-1"],
			WIDE,
		);
		const ys = layout.publicNodes.map((n) => n.y);
		expect(ys[0]).toBe(0);
		expect(ys.toSorted((a, b) => a - b)).toEqual(ys);
		expect(new Set(layout.publicNodes.map((n) => n.x)).size).toBe(1);
		expect(layout.height).toBe((ys.at(-1) ?? 0) + MAP_NODE_H);
		expect(middle(layout.internet?.y)).toBeCloseTo(layout.height / 2);
		expect(middle(layout.gateways[0]?.y)).toBeCloseTo(layout.height / 2);
	});

	test("edges join Internet to gateways and gateways to the public resources they serve, never internal ones", () => {
		const both = [
			{ region: "us-east-1", desired: 1, ready: 1 },
			{ region: "eu-west-1", desired: 2, ready: 2 },
		];
		const layout = mapLayout(
			[
				row("web", { domain: "web.example.dev", regions: both }),
				row("api", { domain: "api.example.dev" }),
				row("queue"),
			],
			["us-east-1", "eu-west-1"],
			WIDE,
		);
		expect(layout.gaps.map((g) => [g.key, g.edges.map((e) => e.key).toSorted(), g.divider])).toEqual([
			["gateways", ["net/eu-west-1", "net/us-east-1"], false],
			["public", ["eu-west-1/web", "us-east-1/api", "us-east-1/web"], false],
			["internal", [], true],
		]);
		expect(layout.gateways.map((g) => [g.region, g.resources, g.replicas])).toEqual([
			["us-east-1", 2, 2],
			["eu-west-1", 1, 2],
		]);
	});

	test("an edge meets the gap's sides at the middle of the nodes it joins", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" })], ["us-east-1"], WIDE);
		const [netGap, pubGap] = layout.gaps;
		const gateway = layout.gateways[0];
		expect(netGap?.x).toBe((layout.internet?.x ?? 0) + MAP_NODE_W);
		expect((netGap?.x ?? 0) + (netGap?.width ?? 0)).toBeCloseTo(gateway?.x ?? 0);
		expect(netGap?.edges[0]?.y1).toBe(middle(layout.internet?.y));
		expect(pubGap?.edges[0]?.y2).toBe(middle(layout.publicNodes[0]?.y));
	});

	test("without public resources the internal column stands alone", () => {
		const layout = mapLayout([row("queue")], ["us-east-1"], WIDE);
		expect(layout.internet).toBeNull();
		expect(layout.gateways).toEqual([]);
		expect(layout.gaps).toEqual([]);
		expect(layout.internalLabel).toEqual({ x: 0, y: 0 });
		expect(layout.internalNodes[0]?.y).toBeGreaterThan(0);
	});

	test("public resources past the limit are counted, not drawn, and unhealthy ones are drawn first", () => {
		const healthy = Array.from({ length: MAP_PUBLIC_LIMIT + 2 }, (_, i) =>
			row(`a-${i.toString().padStart(2, "0")}`, { domain: `a-${i.toString()}.example.dev` }),
		);
		const broken = row("z-broken", { domain: "z.example.dev", status: ResourceStatus.DEGRADED });
		const layout = mapLayout([...healthy, broken], ["us-east-1"], WIDE);
		expect(layout.publicNodes).toHaveLength(MAP_PUBLIC_LIMIT);
		expect(layout.hiddenPublic).toBe(3);
		expect(itemName(layout.publicNodes[0]?.row ?? row("none"))).toBe("z-broken");
		expect(layout.publicMore?.y).toBeGreaterThan(layout.publicNodes.at(-1)?.y ?? 0);
		expect(layout.publicMore?.x).toBe(layout.publicNodes[0]?.x);
		expect(layout.internalMore).toBeNull();
	});

	test("drafts sit in their column after deploying resources, on dashed edges, and add no routes", () => {
		const regions = [create(RegionInfoSchema, { region: "us-east-1", isDefault: true })];
		const drafts = draftItems([draft("new-web"), draft("new-queue", { isPublic: false })], regions);
		const rows = [
			row("web", { domain: "web.example.dev" }),
			row("rolling", { domain: "r.example.dev", status: ResourceStatus.DEPLOYING }),
			...drafts,
		];
		const layout = mapLayout(rows, ["us-east-1"], WIDE);
		expect(layout.publicNodes.map((n) => itemName(n.row))).toEqual(["rolling", "new-web", "web"]);
		expect(layout.internalNodes.map((n) => itemName(n.row))).toEqual(["new-queue"]);
		expect(layout.gateways[0]?.resources).toBe(2);
		const edges = layout.gaps.find((g) => g.key === "public")?.edges ?? [];
		expect(edges.map((e) => [e.key, e.draft])).toEqual([
			["us-east-1/rolling", false],
			["us-east-1/d-new-web", true],
			["us-east-1/web", false],
		]);
	});
});
