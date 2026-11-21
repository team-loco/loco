import type { MessageInitShape } from "@bufbuild/protobuf";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import {
	ResourceOrder,
	ResourceService,
	ResourceStatus,
	type ListWorkspaceResourcesRequestSchema,
	type ResourceFacets,
} from "@gen/loco/resource/v1/resource_pb";

import { collectPages } from "@/lib/collectPages";
import type { Draft } from "@/lib/drafts";
import type { Sort } from "@/lib/sort-parser";

import { ALL, DRAFT, ENV_PREFIX, NOT_DEPLOYED, STATUS_FILTERS } from "./table";

export type ResourcesInput = MessageInitShape<typeof ListWorkspaceResourcesRequestSchema>;

/** The request fields of a status filter. */
export function statusInput(status: string): { status: ResourceStatus; notDeployed: boolean } {
	return {
		status: STATUS_FILTERS.find((f) => f.value === status)?.status ?? ResourceStatus.UNSPECIFIED,
		notDeployed: status === NOT_DEPLOYED,
	};
}

/** The count beside each status filter: all for All, the facets for the API's statuses, drafts for Draft. */
export function statusCounts(
	all: number | undefined,
	facets: ResourceFacets | undefined,
	drafts: number,
): Map<string, number> {
	const counts = new Map<string, number>([[DRAFT, drafts]]);
	if (all !== undefined) counts.set(ALL, all);
	if (facets === undefined) return counts;
	counts.set(NOT_DEPLOYED, facets.notDeployed);
	for (const c of facets.statuses) {
		const filter = STATUS_FILTERS.find((f) => f.status === c.status);
		if (filter !== undefined) counts.set(filter.value, c.count);
	}
	return counts;
}

export interface DraftFilter {
	query: string;
	status: string;
	region: string;
	type?: string;
}

/** The drafts a filter shows: drafts have no status, region or type the API filters, and are services. */
export function shownDrafts(drafts: readonly Draft[], filter: DraftFilter): Draft[] {
	if ((filter.status !== ALL && filter.status !== DRAFT) || filter.region !== ALL) return [];
	if (filter.type !== undefined && filter.type !== ALL && filter.type !== "service") return [];
	const q = filter.query.trim().toLowerCase();
	return drafts.filter((d) => q === "" || d.name.toLowerCase().includes(q) || d.image.toLowerCase().includes(q));
}

const ORDERS: Record<string, ResourceOrder> = {
	name: ResourceOrder.NAME,
	size: ResourceOrder.SIZE,
	created: ResourceOrder.CREATED,
};

/** The order of the Resources page's sort; an environment's column sorts by its status there. */
export function resourcesOrder(sort: Sort<string>, environments: readonly Environment[]) {
	const environment = environments.find((e) => `${ENV_PREFIX}${e.slug}` === sort.id);
	return {
		orderBy: environment === undefined ? (ORDERS[sort.id] ?? ResourceOrder.NAME) : ResourceOrder.STATUS,
		descending: sort.desc,
		environmentId: environment?.id ?? "",
	};
}

const IDS_PAGE_SIZE = 200;
const IDS_POLL_MS = 15_000;

/** The IDs of every resource input matches, following page tokens only past the first page. */
export function useMatchingResourceIds(input: ResourcesInput, enabled: boolean) {
	const transport = useTransport();
	const request = { ...input, pageSize: IDS_PAGE_SIZE, pageToken: "" };
	const key = createConnectQueryKey({
		schema: listWorkspaceResources,
		input: request,
		transport,
		cardinality: "finite",
	});
	return useQuery({
		queryKey: [...key, "ids"],
		enabled,
		placeholderData: keepPreviousData,
		refetchInterval: IDS_POLL_MS,
		queryFn: async ({ signal }) => {
			const client = createClient(ResourceService, transport);
			const ids = await collectPages(async (pageToken) => {
				const page = await client.listWorkspaceResources({ ...request, pageToken }, { signal });
				return { items: page.resources.map((r) => r.id), nextPageToken: page.nextPageToken };
			});
			return new Set(ids);
		},
	});
}
