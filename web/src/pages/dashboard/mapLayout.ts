import { primaryDomain, statusRank } from "@/lib/resource-status";

import type { EnvResource } from "./useDashboardData";

export const MAP_PUBLIC_LIMIT = 8;
const MAP_INTERNAL_LIMIT = 8;
export const MAP_NODE_W = 184;
export const MAP_NODE_H = 66;
export const MAP_MORE_H = 32;
export const MAP_LABEL_H = 28;

const COL_GAP = 16;
const LINE_GAP = 12;
const TIER_GAP = 44;

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
	x1: number;
	y1: number;
	x2: number;
	y2: number;
}

export interface MapLayout {
	width: number;
	height: number;
	internet: MapPoint | null;
	gateways: MapGateway[];
	publicNodes: MapNode[];
	hiddenPublic: number;
	publicMoreY: number;
	internalLabelY: number;
	internalNodes: MapNode[];
	hiddenInternal: number;
	internalMoreY: number;
	edges: MapEdge[];
}

export function perLine(width: number): number {
	return Math.max(1, Math.floor((width + COL_GAP) / (MAP_NODE_W + COL_GAP)));
}

export function placeRow(count: number, top: number, width: number): { points: MapPoint[]; bottom: number } {
	const cols = perLine(width);
	const points: MapPoint[] = [];
	for (let i = 0; i < count; i++) {
		const line = Math.floor(i / cols);
		const inLine = Math.min(cols, count - line * cols);
		const lineW = inLine * MAP_NODE_W + (inLine - 1) * COL_GAP;
		const left = Math.max(0, (width - lineW) / 2);
		points.push({ x: left + (i % cols) * (MAP_NODE_W + COL_GAP), y: top + line * (MAP_NODE_H + LINE_GAP) });
	}
	const lines = Math.ceil(count / cols);
	return { points, bottom: top + lines * MAP_NODE_H + Math.max(0, lines - 1) * LINE_GAP };
}

function edge(key: string, from: MapPoint, to: MapPoint): MapEdge {
	return {
		key,
		x1: from.x + MAP_NODE_W / 2,
		y1: from.y + MAP_NODE_H,
		x2: to.x + MAP_NODE_W / 2,
		y2: to.y,
	};
}

function at<T>(points: MapPoint[], i: number, extra: T): MapPoint & T {
	const p = points[i];
	if (p === undefined) throw new Error(`no map slot ${i.toString()}`);
	return { ...p, ...extra };
}

export function mapLayout(rows: EnvResource[], regionOrder: string[], availableWidth: number): MapLayout {
	const width = Math.max(availableWidth, MAP_NODE_W);
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

	let y = 0;
	let internet: MapPoint | null = null;
	let gateways: MapGateway[] = [];
	let publicNodes: MapNode[] = [];
	let publicMoreY = 0;
	if (shownPublic.length > 0) {
		const net = placeRow(1, y, width);
		internet = at(net.points, 0, {});
		const gw = placeRow(served.length, net.bottom + TIER_GAP, width);
		gateways = served.map((g, i) =>
			at(gw.points, i, {
				region: g.region,
				resources: g.rows.length,
				replicas: g.rows.reduce(
					(sum, r) => sum + r.regions.filter((x) => x.region === g.region).reduce((m, x) => m + x.desired, 0),
					0,
				),
			}),
		);
		const pub = placeRow(shownPublic.length, gw.bottom + TIER_GAP, width);
		publicNodes = shownPublic.map((row, i) => at(pub.points, i, { row }));
		y = pub.bottom;
		if (publicRows.length > shownPublic.length) {
			publicMoreY = y + LINE_GAP;
			y = publicMoreY + MAP_MORE_H;
		}
	}

	let internalLabelY = 0;
	let internalNodes: MapNode[] = [];
	let internalMoreY = 0;
	if (shownInternal.length > 0) {
		internalLabelY = shownPublic.length > 0 ? y + TIER_GAP / 2 : y;
		const internal = placeRow(shownInternal.length, internalLabelY + MAP_LABEL_H + LINE_GAP, width);
		internalNodes = shownInternal.map((row, i) => at(internal.points, i, { row }));
		y = internal.bottom;
		if (internalRows.length > shownInternal.length) {
			internalMoreY = y + LINE_GAP;
			y = internalMoreY + MAP_MORE_H;
		}
	}

	const source = internet;
	const edges: MapEdge[] = [
		...(source === null ? [] : gateways.map((g) => edge(`net/${g.region}`, source, g))),
		...gateways.flatMap((g) =>
			publicNodes
				.filter((n) => n.row.regions.some((r) => r.region === g.region))
				.map((n) => edge(`${g.region}/${n.row.resource.id}`, g, n)),
		),
	];

	return {
		width,
		height: y,
		internet,
		gateways,
		publicNodes,
		hiddenPublic: publicRows.length - shownPublic.length,
		publicMoreY,
		internalLabelY,
		internalNodes,
		hiddenInternal: internalRows.length - shownInternal.length,
		internalMoreY,
		edges,
	};
}
