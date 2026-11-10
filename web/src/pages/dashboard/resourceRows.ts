import { ResourceStatus, ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { DRAFT_RANK, draftIsPublic, draftRegions, type Draft } from "@/lib/drafts";
import { primaryDomain, statusRank } from "@/lib/resource-status";
import { STATUS_FILTERS as RESOURCES_STATUS_FILTERS, writeFilter } from "@/pages/resources/table";

import type { EnvResource, RegionReplicas } from "./useDashboardData";

export interface DraftItem {
	draft: Draft;
	regions: RegionReplicas[];
	isPublic: boolean;
}

export type DashboardItem = EnvResource | DraftItem;

export function isDraftItem(item: DashboardItem): item is DraftItem {
	return "draft" in item;
}

export function draftItems(drafts: readonly Draft[], regions: readonly RegionInfo[]): DraftItem[] {
	return drafts.map((draft) => ({
		draft,
		regions: draftRegions(draft, regions).map((region) => ({ region, desired: 0, ready: 0 })),
		isPublic: draftIsPublic(draft),
	}));
}

export function itemKey(item: DashboardItem): string {
	return isDraftItem(item) ? item.draft.id : item.resource.id;
}

export function itemName(item: DashboardItem): string {
	return isDraftItem(item) ? item.draft.name : item.resource.name;
}

export function itemRank(item: DashboardItem): number {
	return isDraftItem(item) ? DRAFT_RANK : statusRank(item.status);
}

export function itemIsPublic(item: DashboardItem): boolean {
	return isDraftItem(item) ? item.isPublic : primaryDomain(item.resource) !== null;
}

export function itemType(item: DashboardItem): ResourceType {
	return isDraftItem(item) ? ResourceType.SERVICE : item.resource.type;
}

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

export function statusOptions(counts: Map<ResourceStatus, number>, selected: string): typeof STATUS_FILTERS {
	return STATUS_FILTERS.filter((o) => (counts.get(o.status) ?? 0) > 0 || o.value === selected);
}

export interface RowFilter {
	status: string;
	type: string;
	region: string;
	query: string;
}

function matchesQuery(item: DashboardItem, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	const own = isDraftItem(item)
		? [item.draft.name, item.draft.image]
		: [item.resource.name, item.resource.description ?? "", primaryDomain(item.resource) ?? ""];
	const fields = [...own, ...item.regions.map((g) => g.region)];
	return fields.some((f) => f.toLowerCase().includes(q));
}

function matchesStatus(item: DashboardItem, status: string): boolean {
	if (status === ALL) return true;
	return !isDraftItem(item) && STATUS_FILTERS.some((o) => o.value === status && o.status === item.status);
}

function matchesType(item: DashboardItem, type: string): boolean {
	if (type === ALL) return true;
	return TYPE_FILTERS.some((o) => o.value === type && o.type === itemType(item));
}

export function filterRows<T extends DashboardItem>(items: T[], filter: RowFilter): T[] {
	return items.filter(
		(item) =>
			matchesStatus(item, filter.status) &&
			matchesType(item, filter.type) &&
			(filter.region === ALL || item.regions.some((g) => g.region === filter.region)) &&
			matchesQuery(item, filter.query),
	);
}

export function viewAllSearch(envName: string, filter: RowFilter): string {
	const status = RESOURCES_STATUS_FILTERS.some((f) => f.value === filter.status) ? filter.status : ALL;
	const params = new URLSearchParams({ env: envName });
	return writeFilter(params, { query: filter.query.trim(), status, region: filter.region }).toString();
}
