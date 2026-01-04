import { useQuery } from "@connectrpc/connect-query";
import { listUserWorkspaces } from "@/gen/workspace/v1";

export function useWorkspace() {
	const { data } = useQuery(listUserWorkspaces, {});

	const workspace = data?.workspaces?.[0] || null;

	return {
		workspace,
		workspaces: data?.workspaces || [],
	};
}
