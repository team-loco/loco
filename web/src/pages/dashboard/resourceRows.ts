import { ResourceStatus, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { primaryDomain } from "@/lib/resource-status";
import { STATUS_FILTERS as RESOURCES_STATUS_FILTERS, writeFilter } from "@/pages/resources/table";

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

export function statusOptions(counts: Map<ResourceStatus, number>, selected: string): typeof STATUS_FILTERS {
	return STATUS_FILTERS.filter((o) => (counts.get(o.status) ?? 0) > 0 || o.value === selected);
}

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

export function viewAllSearch(envName: string, filter: RowFilter): string {
	const status = RESOURCES_STATUS_FILTERS.some((f) => f.value === filter.status) ? filter.status : ALL;
	const params = new URLSearchParams({ env: envName });
	return writeFilter(params, { query: filter.query.trim(), status, region: filter.region }).toString();
}
