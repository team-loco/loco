import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";

import { useAuth } from "@/auth/AuthProvider";
import { orgMembersOptions, workspaceMembersOptions } from "@/hooks/completeMembers";
import { userLabel } from "@/lib/user";

import type { Creator } from "./model";
import type { Owner } from "./useTokenData";

export function useTokenCreators(owner: Owner): ReadonlyMap<string, Creator> {
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
	const creators = new Map<string, Creator>();
	for (const member of org.data?.members ?? []) {
		if (member.user !== undefined) {
			creators.set(member.user.id, { name: userLabel(member.user), avatarUrl: member.user.avatarUrl });
		}
	}
	for (const member of workspace.data?.members ?? []) {
		creators.set(member.userId, {
			name: userLabel({ name: member.userName, email: member.userEmail }),
			avatarUrl: member.userAvatarUrl,
		});
	}
	if (user !== null) creators.set(user.id, { name: userLabel(user), avatarUrl: user.avatarUrl });
	return creators;
}
