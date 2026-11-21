import { ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { DRAFT_RANK, draftIsPublic, draftRegions, type Draft } from "@/lib/drafts";
import { primaryDomain, statusRank } from "@/lib/resource-status";
import { createSerializer } from "nuqs";

import { FILTER_PARSERS, FILTER_URL_KEYS } from "@/pages/resources/table";

import type { EnvResource, RegionReplicas } from "./useDashboardData";

export interface DraftItem {
	draft: Draft;
	regions: RegionReplicas[];
}

export type DashboardItem = EnvResource | DraftItem;

export function isDraftItem(item: DashboardItem): item is DraftItem {
	return "draft" in item;
}

export function draftItems(
	drafts: readonly Draft[],
	regions: readonly RegionInfo[],
	defaultRegion: string,
): DraftItem[] {
	return drafts.map((draft) => ({
		draft,
		regions: draftRegions(draft, regions, defaultRegion).map((region) => ({ region, desired: 0, ready: 0 })),
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
	return isDraftItem(item) ? draftIsPublic(item.draft) : primaryDomain(item.resource) !== null;
}

export const ALL = "all";

export const TYPE_FILTERS: { value: string; type: ResourceType; label: string }[] = [
	{ value: "service", type: ResourceType.SERVICE, label: "Service" },
	{ value: "database", type: ResourceType.DATABASE, label: "Database" },
	{ value: "cache", type: ResourceType.CACHE, label: "Cache" },
	{ value: "queue", type: ResourceType.QUEUE, label: "Queue" },
	{ value: "blob", type: ResourceType.BLOB, label: "Blob storage" },
	{ value: "function", type: ResourceType.FUNCTION, label: "Function" },
];

export interface RowFilter {
	status: string;
	type: string;
	region: string;
	query: string;
}

const serializeFilter = createSerializer(FILTER_PARSERS, { urlKeys: FILTER_URL_KEYS });

export function viewAllSearch(envName: string, filter: RowFilter): string {
	return serializeFilter(new URLSearchParams({ env: envName }), {
		query: filter.query.trim(),
		status: filter.status,
		region: filter.region,
	});
}
