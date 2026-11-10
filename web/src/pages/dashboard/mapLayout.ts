import { primaryDomain, statusRank } from "@/lib/resource-status";

import type { EnvResource } from "./useDashboardData";

export const MAP_PUBLIC_LIMIT = 8;
const MAP_INTERNAL_LIMIT = 8;
export const MAP_NODE_W = 184;
export const MAP_NODE_H = 66;
export const MAP_MORE_H = 32;
export const MAP_LABEL_H = 28;
export const MAP_MIN_GAP = 56;

const ROW_GAP = 12;

export interface MapPoint {
	x: number;
	y: number;
}

export interface MapGateway extends MapPoint {
	region: string;
	resources: number;
	replicas: number;
}

export interface MapNode extends MapPoint {
	row: EnvResource;
}

export interface MapEdge {
	key: string;
	y1: number;
	y2: number;
}

export interface MapGap {
	key: string;
	x: number;
	width: number;
	edges: MapEdge[];
	divider: boolean;
}

export interface MapLayout {
	width: number;
	height: number;
	internet: MapPoint | null;
	gateways: MapGateway[];
	publicNodes: MapNode[];
	hiddenPublic: number;
	publicMore: MapPoint | null;
	internalLabel: MapPoint | null;
	internalNodes: MapNode[];
	hiddenInternal: number;
	internalMore: MapPoint | null;
	gaps: MapGap[];
}

interface Column {
	key: string;
	count: number;
	label: boolean;
	more: boolean;
}

interface PlacedColumn {
	label: MapPoint | null;
	nodes: MapPoint[];
	more: MapPoint | null;
}

function columnHeight(c: Column): number {
	return (
		(c.label ? MAP_LABEL_H + ROW_GAP : 0) +
		c.count * MAP_NODE_H +
		(c.count - 1) * ROW_GAP +
		(c.more ? ROW_GAP + MAP_MORE_H : 0)
	);
}

function placeColumn(c: Column, x: number, height: number): PlacedColumn {
	let y = (height - columnHeight(c)) / 2;
	let label: MapPoint | null = null;
	if (c.label) {
		label = { x, y };
		y += MAP_LABEL_H + ROW_GAP;
	}
	const nodes = Array.from({ length: c.count }, (_, i) => ({ x, y: y + i * (MAP_NODE_H + ROW_GAP) }));
	const more = c.more ? { x, y: y + c.count * (MAP_NODE_H + ROW_GAP) } : null;
	return { label, nodes, more };
}

function at<T>(points: MapPoint[], i: number, extra: T): MapPoint & T {
	const p = points[i];
	if (p === undefined) throw new Error(`no map slot ${i.toString()}`);
	return { ...p, ...extra };
}

function middle(p: MapPoint): number {
	return p.y + MAP_NODE_H / 2;
}

export function mapLayout(rows: EnvResource[], regionOrder: string[], availableWidth: number): MapLayout {
	const ranked = rows.toSorted(
		(a, b) => statusRank(a.status) - statusRank(b.status) || a.resource.name.localeCompare(b.resource.name),
	);
	const publicRows = ranked.filter((r) => primaryDomain(r.resource) !== null);
	const internalRows = ranked.filter((r) => primaryDomain(r.resource) === null);
	const shownPublic = publicRows.slice(0, MAP_PUBLIC_LIMIT);
	const shownInternal = internalRows.slice(0, MAP_INTERNAL_LIMIT);

	const regionNames = new Set(regionOrder);
	for (const row of shownPublic) for (const g of row.regions) regionNames.add(g.region);
	const served = [...regionNames]
		.map((region) => ({ region, rows: shownPublic.filter((r) => r.regions.some((g) => g.region === region)) }))
		.filter((g) => g.rows.length > 0);

	const columns: Column[] = [];
	if (shownPublic.length > 0) {
		columns.push(
			{ key: "internet", count: 1, label: false, more: false },
			{ key: "gateways", count: served.length, label: false, more: false },
			{ key: "public", count: shownPublic.length, label: false, more: publicRows.length > shownPublic.length },
		);
	}
	if (shownInternal.length > 0) {
		columns.push({
			key: "internal",
			count: shownInternal.length,
			label: true,
			more: internalRows.length > shownInternal.length,
		});
	}

	const n = columns.length;
	const width = Math.max(availableWidth, n * MAP_NODE_W + Math.max(0, n - 1) * MAP_MIN_GAP);
	const gap = n > 1 ? (width - n * MAP_NODE_W) / (n - 1) : 0;
	const height = Math.max(0, ...columns.map(columnHeight));
	const xs = columns.map((_, i) => i * (MAP_NODE_W + gap));
	const placed = new Map(columns.map((c, i) => [c.key, placeColumn(c, xs[i] ?? 0, height)]));

	const net = placed.get("internet");
	const internet = net === undefined ? null : at(net.nodes, 0, {});
	const gateways = served.map((g, i) =>
		at(placed.get("gateways")?.nodes ?? [], i, {
			region: g.region,
			resources: g.rows.length,
			replicas: g.rows.reduce(
				(sum, r) => sum + r.regions.filter((x) => x.region === g.region).reduce((m, x) => m + x.desired, 0),
				0,
			),
		}),
	);
	const pub = placed.get("public");
	const publicNodes = shownPublic.map((row, i) => at(pub?.nodes ?? [], i, { row }));
	const internal = placed.get("internal");
	const internalNodes = shownInternal.map((row, i) => at(internal?.nodes ?? [], i, { row }));

	const edgesInto = (key: string): MapEdge[] => {
		if (key === "gateways" && internet !== null)
			return gateways.map((g) => ({ key: `net/${g.region}`, y1: middle(internet), y2: middle(g) }));
		if (key === "public")
			return gateways.flatMap((g) =>
				publicNodes
					.filter((p) => p.row.regions.some((r) => r.region === g.region))
					.map((p) => ({ key: `${g.region}/${p.row.resource.id}`, y1: middle(g), y2: middle(p) })),
			);
		return [];
	};
	const gaps = columns.slice(1).map((c, i) => ({
		key: c.key,
		x: (xs[i] ?? 0) + MAP_NODE_W,
		width: gap,
		edges: edgesInto(c.key),
		divider: c.key === "internal",
	}));

	return {
		width,
		height,
		internet,
		gateways,
		publicNodes,
		hiddenPublic: publicRows.length - shownPublic.length,
		publicMore: pub?.more ?? null,
		internalLabel: internal?.label ?? null,
		internalNodes,
		hiddenInternal: internalRows.length - shownInternal.length,
		internalMore: internal?.more ?? null,
		gaps,
	};
}
