import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { queryOptions } from "@tanstack/react-query";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { ListUserOrgsResponseSchema, OrgService } from "@gen/loco/org/v1/org_pb";
import { collectPages } from "@/lib/collectPages";

export function userOrgsOptions(transport: Transport, userId: string) {
	const input = { userId };
	const key = createConnectQueryKey({ schema: listUserOrgs, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }) => {
			const client = createClient(OrgService, transport);
			const orgs = await collectPages(async (pageToken) => {
				const page = await client.listUserOrgs({ ...input, pageToken }, { signal });
				return { items: page.orgs, nextPageToken: page.nextPageToken };
			});
			return create(ListUserOrgsResponseSchema, { orgs });
		},
		enabled: userId !== "",
	});
}
