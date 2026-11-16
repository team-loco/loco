import type { ServiceGraphEdge } from "@gen/loco/observability/v1/observability_pb";

import { MAP_NODE_H, MAP_NODE_W, type MapLayout, type MapPoint } from "./mapLayout";
import { itemName } from "./resourceRows";

export function callLabel(edge: ServiceGraphEdge, rangeMs: number): string {
	const rate = rangeMs > 0 ? Number(edge.requests) / (rangeMs / 1000) : 0;
	const errors = edge.requests > 0n ? (Number(edge.errors) / Number(edge.requests)) * 100 : 0;
	return `${rate.toLocaleString("en-US", { maximumSignificantDigits: 3 })} req/s · ${errors.toLocaleString("en-US", { maximumFractionDigits: 1 })}% errors · p95 ${edge.p95Ms.toLocaleString("en-US", { maximumFractionDigits: 1 })} ms`;
}

export function serviceGraph(layout: MapLayout, edges: ServiceGraphEdge[], rangeMs: number) {
	const nodes = new Map<string, MapPoint>(
		[...layout.publicNodes, ...layout.internalNodes].map((node) => [itemName(node.row), node]),
	);
	const peers = [...new Set(edges.flatMap((edge) => [edge.from, edge.to]))].filter((name) => !nodes.has(name));
	const right = Math.max(0, ...[...nodes.values()].map((node) => node.x + MAP_NODE_W));
	let peerY = -160;
	const external = peers.map((name) => {
		const caller = edges.find((edge) => edge.to === name);
		peerY = Math.max(peerY + 160, nodes.get(caller?.from ?? "")?.y ?? 0);
		return { name, x: right + 360, y: peerY };
	});
	for (const node of external) nodes.set(node.name, node);
	const calls = edges.map((edge, i) => {
		const from = nodes.get(edge.from);
		const to = nodes.get(edge.to);
		if (from === undefined || to === undefined) throw new Error("Missing service graph node");
		const y1 = from.y + MAP_NODE_H / 2 + 12;
		const y2 = to.y + MAP_NODE_H / 2 - 12;
		const sameColumn = from.x === to.x;
		const forward = from.x < to.x;
		const x1 = from.x + (sameColumn || forward ? MAP_NODE_W : 0);
		const x2 = to.x + (sameColumn || !forward ? MAP_NODE_W : 0);
		const bend = sameColumn ? x1 + 80 + i * 12 : (x1 + x2) / 2;
		const self = edge.from === edge.to;
		const reverse = edge.from > edge.to && edges.some((other) => other.from === edge.to && other.to === edge.from);
		return {
			key: JSON.stringify([edge.from, edge.to]),
			name: `${edge.from} → ${edge.to}`,
			label: callLabel(edge, rangeMs),
			x: sameColumn ? bend + 8 : (x1 + x2) / 2 - 110,
			y: self ? y1 + 45 : (y1 + y2) / 2 - 24 + (reverse ? 72 : 0),
			path: self
				? `M${x1},${y1 - 12} C${bend + 80},${y1 - 60} ${bend + 80},${y1 + 60} ${x2},${y1 + 12}`
				: `M${x1},${y1} C${bend},${y1} ${bend},${y2} ${x2},${y2}`,
		};
	});
	return {
		external,
		calls,
		width: Math.max(layout.width, ...external.map((node) => node.x + MAP_NODE_W), ...calls.map((call) => call.x + 230)),
		height: Math.max(
			layout.height,
			...external.map((node) => node.y + MAP_NODE_H),
			...calls.map((call) => call.y + 60),
		),
	};
}
