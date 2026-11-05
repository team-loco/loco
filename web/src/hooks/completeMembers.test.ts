import { describe, expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { OrgService } from "@gen/loco/org/v1/org_pb";
import { invitationsOptions, orgMembersOptions } from "./completeMembers";

describe("complete org lists", () => {
	test("loads member 201 and invitation 201 through the cursor", async () => {
		const transport = createRouterTransport((router) => {
			router.service(OrgService, {
				listOrgMembers: (request) =>
					request.pageToken === ""
						? { members: Array.from({ length: 200 }, () => ({})), nextPageToken: "next" }
						: { members: [{}] },
				listInvitations: (request) =>
					request.pageToken === ""
						? { invitations: Array.from({ length: 200 }, () => ({})), nextPageToken: "next" }
						: { invitations: [{}] },
			});
		});
		const client = new QueryClient();
		try {
			expect((await client.query(orgMembersOptions(transport, "org"))).members).toHaveLength(201);
			expect((await client.query(invitationsOptions(transport, "org"))).invitations).toHaveLength(201);
		} finally {
			client.clear();
		}
	});
});
