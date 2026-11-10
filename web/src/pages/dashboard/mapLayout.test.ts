import { describe, expect, test } from "bun:test";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { MAP_NODE_H, MAP_NODE_W, MAP_PUBLIC_LIMIT, mapLayout, perLine, placeRow } from "./mapLayout";
import { row } from "./testRows";

const WIDE = 1200;

function centre(x: number): number {
	return x + MAP_NODE_W / 2;
}

describe("placeRow", () => {
	test("a single line is centred in the width", () => {
		const { points } = placeRow(3, 0, WIDE);
		const left = points[0]?.x ?? 0;
		const right = (points[2]?.x ?? 0) + MAP_NODE_W;
		expect(left).toBeCloseTo(WIDE - right);
		expect(new Set(points.map((p) => p.y)).size).toBe(1);
	});

	test("nodes past the line capacity wrap onto a new line, which is centred on its own", () => {
		const width = 3 * MAP_NODE_W + 40;
		expect(perLine(width)).toBe(3);
		const { points, bottom } = placeRow(4, 10, width);
		const [a, , , d] = points;
		expect(a?.y).toBe(10);
		expect(d?.y).toBeGreaterThanOrEqual(10 + MAP_NODE_H);
		expect(centre(d?.x ?? 0)).toBeCloseTo(width / 2);
		expect(bottom).toBe((d?.y ?? 0) + MAP_NODE_H);
	});

	test("a width narrower than one node still places one node per line", () => {
		const { points } = placeRow(2, 0, MAP_NODE_W / 2);
		expect(points.map((p) => p.x)).toEqual([0, 0]);
		expect(points[1]?.y).toBeGreaterThan(points[0]?.y ?? 0);
	});
});

describe("mapLayout", () => {
	test("tiers run top to bottom: Internet, gateways, public resources, internal resources", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("queue")], ["us-east-1"], WIDE);
		const gateway = layout.gateways[0];
		const web = layout.publicNodes[0];
		const queue = layout.internalNodes[0];
		expect(layout.internet).not.toBeNull();
		expect(gateway?.y).toBeGreaterThan(layout.internet?.y ?? 0);
		expect(web?.y).toBeGreaterThan(gateway?.y ?? 0);
		expect(layout.internalLabelY).toBeGreaterThan(web?.y ?? 0);
		expect(queue?.y).toBeGreaterThan(layout.internalLabelY);
		expect(layout.height).toBeGreaterThanOrEqual((queue?.y ?? 0) + MAP_NODE_H);
		expect(centre(layout.internet?.x ?? 0)).toBeCloseTo(WIDE / 2);
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
		expect(layout.edges.map((e) => e.key).toSorted()).toEqual(
			["net/us-east-1", "net/eu-west-1", "us-east-1/web", "us-east-1/api", "eu-west-1/web"].toSorted(),
		);
		expect(layout.gateways.map((g) => [g.region, g.resources, g.replicas])).toEqual([
			["us-east-1", 2, 2],
			["eu-west-1", 1, 2],
		]);
	});

	test("without public resources there is no Internet or gateway tier", () => {
		const layout = mapLayout([row("queue")], ["us-east-1"], WIDE);
		expect(layout.internet).toBeNull();
		expect(layout.gateways).toEqual([]);
		expect(layout.edges).toEqual([]);
		expect(layout.internalLabelY).toBe(0);
	});

	test("public resources past the limit are counted, not drawn, and unhealthy ones are drawn first", () => {
		const healthy = Array.from({ length: MAP_PUBLIC_LIMIT + 2 }, (_, i) =>
			row(`a-${i.toString().padStart(2, "0")}`, { domain: `a-${i.toString()}.example.dev` }),
		);
		const broken = row("z-broken", { domain: "z.example.dev", status: ResourceStatus.DEGRADED });
		const layout = mapLayout([...healthy, broken], ["us-east-1"], WIDE);
		expect(layout.publicNodes).toHaveLength(MAP_PUBLIC_LIMIT);
		expect(layout.hiddenPublic).toBe(3);
		expect(layout.publicNodes[0]?.row.resource.name).toBe("z-broken");
		expect(layout.publicMoreY).toBeGreaterThan(layout.publicNodes.at(-1)?.y ?? 0);
	});
});
