import { useQuery } from "@connectrpc/connect-query";
import { getUserWorkspaces } from "@/gen/workspace/v1";

export function useWorkspace() {
	const { data } = useQuery(getUserWorkspaces, {});

	const workspace = data?.workspaces?.[0] || null;

	return {
		workspace,
		workspaces: data?.workspaces || [],
	};
}
