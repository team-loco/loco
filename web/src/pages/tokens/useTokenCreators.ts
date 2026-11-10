import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";

import { useAuth } from "@/auth/AuthProvider";
import { orgMembersOptions, workspaceMembersOptions } from "@/hooks/completeMembers";
import { userLabel } from "@/lib/user";

import type { Owner } from "./useTokenData";

export function useTokenCreators(owner: Owner): ReadonlyMap<string, string> {
	const transport = useTransport();
	const { user } = useAuth();
	const org = useQuery({
		...orgMembersOptions(transport, owner.id ?? ""),
		enabled: owner.key === "org" && owner.canList,
	});
	const workspace = useQuery({
		...workspaceMembersOptions(transport, owner.id ?? ""),
		enabled: owner.key === "workspace" && owner.canList,
	});
	const names = new Map<string, string>();
	for (const member of org.data?.members ?? []) {
		if (member.user !== undefined) names.set(member.user.id, userLabel(member.user));
	}
	for (const member of workspace.data?.members ?? []) {
		names.set(member.userId, userLabel({ name: member.userName, email: member.userEmail }));
	}
	if (user !== null) names.set(user.id, userLabel(user));
	return names;
}
