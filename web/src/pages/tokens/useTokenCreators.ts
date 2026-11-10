import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";

import { useAuth } from "@/auth/AuthProvider";
import { orgMembersOptions, workspaceMembersOptions } from "@/hooks/completeMembers";

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
		if (member.user !== undefined) names.set(member.user.id, member.user.name || member.user.email || member.user.id);
	}
	for (const member of workspace.data?.members ?? []) {
		names.set(member.userId, member.userName || member.userEmail || member.userId);
	}
	if (user !== null) names.set(user.id, user.name || user.email || user.id);
	return names;
}
