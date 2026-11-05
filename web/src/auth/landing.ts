import { type Transport, createClient } from "@connectrpc/connect";
import { OrgService } from "@gen/loco/org/v1/org_pb";
import { UserService } from "@gen/loco/user/v1/user_pb";

import { takeNextPath } from "./next";

export async function landingPath(transport: Transport): Promise<string> {
	const { user } = await createClient(UserService, transport).whoAmI({});
	if (user === undefined) throw new Error("Your account could not be loaded");

	const next = takeNextPath();
	await createClient(OrgService, transport).listUserOrgs({ userId: user.id, pageSize: 1, createIfMissing: true });
	return next ?? "/dashboard";
}
