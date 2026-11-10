import { ResourceStatus, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { primaryDomain, statusRank } from "@/lib/resource-status";

import type { EnvResource } from "./useDashboardData";

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

export interface RowFilter {
	status: string;
	type: string;
	region: string;
	query: string;
}

function matchesQuery(item: EnvResource, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	const { resource } = item;
	const fields = [
		resource.name,
		resource.description ?? "",
		primaryDomain(resource) ?? "",
		...item.regions.map((g) => g.region),
	];
	return fields.some((f) => f.toLowerCase().includes(q));
}

function matchesStatus(item: EnvResource, status: string): boolean {
	if (status === ALL) return true;
	return STATUS_FILTERS.some((o) => o.value === status && o.status === item.status);
}

function matchesType(item: EnvResource, type: string): boolean {
	if (type === ALL) return true;
	return TYPE_FILTERS.some((o) => o.value === type && o.type === item.resource.type);
}

export function filterRows(items: EnvResource[], filter: RowFilter): EnvResource[] {
	return items.filter(
		(item) =>
			matchesStatus(item, filter.status) &&
			matchesType(item, filter.type) &&
			(filter.region === ALL || item.regions.some((g) => g.region === filter.region)) &&
			matchesQuery(item, filter.query),
	);
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
	publicNodes: { row: EnvResource; y: number }[];
	hiddenPublic: number;
	moreY: number;
	internal: EnvResource[];
	hiddenInternal: number;
	edges: MapEdge[];
}

export function mapLayout(rows: EnvResource[], regionOrder: string[]): MapLayout {
	const ranked = rows.toSorted(
		(a, b) => statusRank(a.status) - statusRank(b.status) || a.resource.name.localeCompare(b.resource.name),
	);
	const publicRows = ranked.filter((r) => primaryDomain(r.resource) !== null);
	const internalRows = ranked.filter((r) => primaryDomain(r.resource) === null);
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
			.map((n) => ({ from: g.region, to: n.row.resource.id, y1: g.y, y2: n.y })),
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
