import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { useQueries } from "@tanstack/react-query";

export interface ResourceCount {
	count: number;
	more: boolean;
}

const PAGE_SIZE = 200;

export function useResourceCounts(workspaceIds: string[]) {
	const transport = useTransport();
	const results = useQueries({
		queries: workspaceIds.map((workspaceId) =>
			createQueryOptions(listWorkspaceResources, { workspaceId, pageSize: PAGE_SIZE }, { transport }),
		),
	});

	const counts = new Map<string, ResourceCount>();
	workspaceIds.forEach((id, i) => {
		const data = results[i]?.data;
		if (data) counts.set(id, { count: data.resources.length, more: data.nextPageToken !== "" });
	});
	const isLoading = results.some((r) => r.isLoading);

	return { counts, isLoading };
}

export function formatResourceCount(c: ResourceCount | undefined): string {
	if (!c) return "—";
	if (c.count === 0) return "Empty";
	const n = c.more ? `${c.count.toString()}+` : c.count.toString();
	return `${n} ${c.count === 1 && !c.more ? "resource" : "resources"}`;
}
