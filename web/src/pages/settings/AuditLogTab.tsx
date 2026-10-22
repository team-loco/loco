import type { Organization, User } from "@gen/loco/org/v1/org_pb";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { ActivityPanel } from "./ActivityPanel";
import type { AuditUser } from "./auditEvents";

export function AuditLogTab({
	org,
	workspaces,
	users,
}: {
	org: Organization;
	workspaces: Workspace[];
	users: User[];
}) {
	const usersById = new Map<string, AuditUser>(users.map((u) => [u.id, { name: u.name, email: u.email }]));
	const names = new Map<string, string>([[org.id, org.name], ...workspaces.map((w): [string, string] => [w.id, w.name])]);
	return (
		<ActivityPanel
			orgId={org.id}
			title="Audit log"
			description="Changes made in this organization, newest first. Only organization admins can see this."
			users={usersById}
			names={names}
		/>
	);
}
