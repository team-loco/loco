import { Code, ConnectError, type Transport, createClient } from "@connectrpc/connect";
import { OrgService } from "@gen/loco/org/v1/org_pb";
import { UserService } from "@gen/loco/user/v1/user_pb";
import { WorkspaceService } from "@gen/loco/workspace/v1/workspace_pb";

import { workspacePath } from "@/lib/routes";

import { takeNextPath } from "./next";

function alreadyExists(err: unknown): boolean {
	return err instanceof ConnectError && err.code === Code.AlreadyExists;
}

async function ensureDefaultOrg(transport: Transport, userId: string, email: string): Promise<string> {
	const orgs = createClient(OrgService, transport);
	try {
		const created = await orgs.createOrg({ name: email });
		return created.orgId;
	} catch (err) {
		if (!alreadyExists(err)) throw err;
		const { orgs: existing } = await orgs.listUserOrgs({ userId });
		const first = existing[0];
		if (first === undefined) throw new Error("Your organization exists but could not be loaded");
		return first.id;
	}
}

async function ensureDefaultWorkspace(transport: Transport, orgId: string): Promise<string> {
	const workspaces = createClient(WorkspaceService, transport);
	try {
		const created = await workspaces.createWorkspace({ orgId, name: "default" });
		return created.workspaceId;
	} catch (err) {
		if (!alreadyExists(err)) throw err;
		const { workspaces: existing } = await workspaces.listOrgWorkspaces({ orgId });
		const first = existing[0];
		if (first === undefined) throw new Error("Your workspace exists but could not be loaded");
		return first.id;
	}
}

export async function landingPath(
	transport: Transport,
	knownUserId: string | null,
	report: (message: string) => void,
): Promise<string> {
	const { user } = await createClient(UserService, transport).whoAmI({});
	const userId = knownUserId ?? user?.id;
	if (userId === undefined) throw new Error("Your account could not be loaded");

	const next = takeNextPath();
	const { orgs } = await createClient(OrgService, transport).listUserOrgs({ userId });
	if (orgs.length > 0) return next ?? "/dashboard";

	report("Creating your organization…");
	const orgId = await ensureDefaultOrg(transport, userId, user?.email ?? userId);
	report("Creating your workspace…");
	const workspaceId = await ensureDefaultWorkspace(transport, orgId);
	return next ?? workspacePath(orgId, workspaceId);
}
