import { ResourceStatus, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { isPrivate, type Draft } from "./drafts";
import { statusRank } from "./format";
import type { EnvResource, RegionReplicas } from "./useDashboardData";

export const ALL = "all";

export const STATUS_FILTERS: { value: string; status: ResourceStatus; label: string }[] = [
	{ value: "healthy", status: ResourceStatus.HEALTHY, label: "Healthy" },
	{ value: "deploying", status: ResourceStatus.DEPLOYING, label: "Deploying" },
	{ value: "degraded", status: ResourceStatus.DEGRADED, label: "Degraded" },
	{ value: "unavailable", status: ResourceStatus.UNAVAILABLE, label: "Unavailable" },
	{ value: "unknown", status: ResourceStatus.UNKNOWN, label: "Unknown" },
];

export const TYPE_FILTERS: { value: string; type: ResourceType; label: string }[] = [
	{ value: "service", type: ResourceType.SERVICE, label: "Service" },
	{ value: "database", type: ResourceType.DATABASE, label: "Database" },
	{ value: "function", type: ResourceType.FUNCTION, label: "Function" },
	{ value: "cache", type: ResourceType.CACHE, label: "Cache" },
	{ value: "queue", type: ResourceType.QUEUE, label: "Queue" },
	{ value: "blob", type: ResourceType.BLOB, label: "Blob" },
];

export interface ResourceRow {
	key: string;
	name: string;
	description: string;
	status: ResourceStatus | "draft";
	type: ResourceType;
	domain: string | null;
	regions: RegionReplicas[];
	item: EnvResource | undefined;
}

export interface RowFilter {
	status: string;
	type: string;
	region: string;
	query: string;
}

export function resourceRow(item: EnvResource): ResourceRow {
	const { resource } = item;
	const domain = resource.domains.find((d) => d.isPrimary) ?? resource.domains[0];
	return {
		key: resource.id,
		name: resource.name,
		description: resource.description ?? "",
		status: item.status,
		type: resource.type,
		domain: domain?.domain ?? null,
		regions: item.regions,
		item,
	};
}

export function draftRow(draft: Draft): ResourceRow {
	return {
		key: draft.id,
		name: draft.name,
		description: draft.image,
		status: "draft",
		type: ResourceType.SERVICE,
		domain: isPrivate(draft) || draft.sub === "" ? null : draft.sub,
		regions: [{ region: draft.region, desired: 0, ready: 0 }],
		item: undefined,
	};
}

function matchesQuery(row: ResourceRow, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	const fields = [row.name, row.description, row.domain ?? "", ...row.regions.map((g) => g.region)];
	return fields.some((f) => f.toLowerCase().includes(q));
}

function matchesStatus(row: ResourceRow, status: string): boolean {
	if (status === ALL) return true;
	return STATUS_FILTERS.some((o) => o.value === status && o.status === row.status);
}

function matchesType(row: ResourceRow, type: string): boolean {
	if (type === ALL) return true;
	return TYPE_FILTERS.some((o) => o.value === type && o.type === row.type);
}

export function filterRows(rows: ResourceRow[], filter: RowFilter): ResourceRow[] {
	return rows.filter(
		(row) =>
			matchesStatus(row, filter.status) &&
			matchesType(row, filter.type) &&
			(filter.region === ALL || row.regions.some((g) => g.region === filter.region)) &&
			matchesQuery(row, filter.query),
	);
}

export function rowRank(row: ResourceRow): number {
	return row.status === "draft" ? statusRank(ResourceStatus.HEALTHY) + 1 : statusRank(row.status);
}

export const MAP_PUBLIC_LIMIT = 8;
export const MAP_INTERNAL_LIMIT = 8;

const NODE_H = 66;
const NODE_GAP = 12;
const MAP_PAD = 8;

export interface MapGateway {
	region: string;
	y: number;
	resources: number;
	replicas: number;
}

export interface MapEdge {
	from: string;
	to: string;
	y1: number;
	y2: number;
}

export interface MapLayout {
	height: number;
	nodeHeight: number;
	internetY: number;
	gateways: MapGateway[];
	publicNodes: { row: ResourceRow; y: number }[];
	hiddenPublic: number;
	moreY: number;
	internal: ResourceRow[];
	hiddenInternal: number;
	edges: MapEdge[];
}

export function mapLayout(rows: ResourceRow[], regionOrder: string[]): MapLayout {
	const ranked = rows.toSorted((a, b) => rowRank(a) - rowRank(b) || a.name.localeCompare(b.name));
	const publicRows = ranked.filter((r) => r.domain !== null);
	const internalRows = ranked.filter((r) => r.domain === null);
	const shown = publicRows.slice(0, MAP_PUBLIC_LIMIT);
	const slot = (i: number) => MAP_PAD + i * (NODE_H + NODE_GAP);
	const publicNodes = shown.map((row, i) => ({ row, y: slot(i) }));

	const regionNames = new Set(regionOrder);
	for (const { row } of publicNodes) for (const g of row.regions) regionNames.add(g.region);

	const gateways: MapGateway[] = [];
	for (const region of regionNames) {
		const mine = publicNodes.filter((n) => n.row.regions.some((g) => g.region === region));
		if (mine.length === 0) continue;
		const centre = mine.reduce((sum, n) => sum + n.y, 0) / mine.length;
		const replicas = mine.reduce(
			(sum, n) => sum + n.row.regions.filter((g) => g.region === region).reduce((m, g) => m + g.desired, 0),
			0,
		);
		gateways.push({ region, y: centre, resources: mine.length, replicas });
	}
	gateways.sort((a, b) => a.y - b.y);
	gateways.forEach((g, i) => {
		const above = gateways[i - 1];
		if (above !== undefined) g.y = Math.max(g.y, above.y + NODE_H + NODE_GAP);
	});

	const edges: MapEdge[] = gateways.flatMap((g) =>
		publicNodes
			.filter((n) => n.row.regions.some((r) => r.region === g.region))
			.map((n) => ({ from: g.region, to: n.row.key, y1: g.y, y2: n.y })),
	);
	const slots = Math.max(shown.length + (publicRows.length > shown.length ? 1 : 0), 1);
	const bottom = Math.max(slot(slots - 1), gateways.at(-1)?.y ?? 0);
	const gatewaysCentre = gateways.length === 0 ? MAP_PAD : gateways.reduce((sum, g) => sum + g.y, 0) / gateways.length;

	return {
		height: bottom + NODE_H + MAP_PAD,
		nodeHeight: NODE_H,
		internetY: gatewaysCentre,
		gateways,
		publicNodes,
		hiddenPublic: publicRows.length - shown.length,
		moreY: slot(shown.length),
		internal: internalRows.slice(0, MAP_INTERNAL_LIMIT),
		hiddenInternal: Math.max(0, internalRows.length - MAP_INTERNAL_LIMIT),
		edges,
	};
}
