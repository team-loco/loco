import { ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { DRAFT_RANK, draftIsPublic, draftRegions, type Draft } from "@/lib/drafts";
import { primaryDomain, statusRank } from "@/lib/resource-status";
import { createSerializer } from "nuqs";

import { DRAFT, FILTER_PARSERS, FILTER_URL_KEYS, NOT_DEPLOYED, STATUS_FILTERS } from "@/pages/resources/table";

import type { EnvResource, RegionReplicas } from "./useDashboardData";

export interface DraftItem {
	draft: Draft;
	regions: RegionReplicas[];
}

export type DashboardItem = EnvResource | DraftItem;

export function isDraftItem(item: DashboardItem): item is DraftItem {
	return "draft" in item;
}

export function draftItems(drafts: readonly Draft[], regions: readonly RegionInfo[]): DraftItem[] {
	return drafts.map((draft) => ({
		draft,
		regions: draftRegions(draft, regions).map((region) => ({ region, desired: 0, ready: 0 })),
	}));
}

export function itemKey(item: DashboardItem): string {
	return isDraftItem(item) ? item.draft.id : item.resource.id;
}

export function itemName(item: DashboardItem): string {
	return isDraftItem(item) ? item.draft.name : item.resource.displayName;
}

export function itemRank(item: DashboardItem): number {
	return isDraftItem(item) ? DRAFT_RANK : statusRank(item.status);
}

export function itemIsPublic(item: DashboardItem): boolean {
	return isDraftItem(item) ? draftIsPublic(item.draft) : primaryDomain(item.resource) !== null;
}

export function itemType(item: DashboardItem): ResourceType {
	return isDraftItem(item) ? ResourceType.SERVICE : item.resource.type;
}

export const ALL = "all";

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

function matchesQuery(item: DashboardItem, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	const own = isDraftItem(item)
		? [item.draft.name, item.draft.image]
		: [
				item.resource.displayName,
				item.resource.name,
				item.resource.description ?? "",
				primaryDomain(item.resource) ?? "",
			];
	const fields = [...own, ...item.regions.map((g) => g.region)];
	return fields.some((f) => f.toLowerCase().includes(q));
}

export function itemStatusFilter(item: DashboardItem): string {
	if (isDraftItem(item)) return DRAFT;
	if (item.neverDeployed) return NOT_DEPLOYED;
	return STATUS_FILTERS.find((f) => f.status === item.status)?.value ?? ALL;
}

function matchesStatus(item: DashboardItem, status: string): boolean {
	return status === ALL || itemStatusFilter(item) === status;
}

function matchesType(item: DashboardItem, type: string): boolean {
	if (type === ALL) return true;
	return TYPE_FILTERS.some((o) => o.value === type && o.type === itemType(item));
}

// The sorted row model falls back to input order on ties, in either direction,
// so returning rows in name order makes every tie sort by name ascending.
export function filterRows<T extends DashboardItem>(items: T[], filter: RowFilter): T[] {
	return items
		.filter(
			(item) =>
				matchesStatus(item, filter.status) &&
				matchesType(item, filter.type) &&
				(filter.region === ALL || item.regions.some((g) => g.region === filter.region)) &&
				matchesQuery(item, filter.query),
		)
		.toSorted((a, b) => itemName(a).localeCompare(itemName(b)));
}

const serializeFilter = createSerializer(FILTER_PARSERS, { urlKeys: FILTER_URL_KEYS });

export function viewAllSearch(envName: string, filter: RowFilter): string {
	return serializeFilter(new URLSearchParams({ env: envName }), {
		query: filter.query.trim(),
		status: filter.status,
		region: filter.region,
	});
}
