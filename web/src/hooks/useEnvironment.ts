import { useQuery } from "@connectrpc/connect-query";
import { EnvironmentType, type Environment } from "@gen/loco/environment/v1/environment_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { readStorage, writeStorage } from "@/lib/storage";

const storageKey = (workspaceId: string) => `loco:env:v1:${workspaceId}`;

export function useEnvironments() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const scopes = useMyScopes();
	const [params, setParams] = useSearchParams();
	const grants =
		activeOrgId !== null && activeWorkspaceId !== null
			? scopes.resourceGrants(activeOrgId, activeWorkspaceId)
			: undefined;
	// GetScopes does not say which workspace a granted resource is in; only a grant in this
	// workspace lets the user list its environments.
	const granted = useTanstackQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: typeof grants === "object" && grants.length > 0,
	});
	const { data, isLoading: environmentsLoading } = useQuery(
		listEnvironments,
		{ workspaceId: activeWorkspaceId ?? "" },
		{
			enabled:
				activeOrgId !== null &&
				activeWorkspaceId !== null &&
				(scopes.holds(activeOrgId, activeWorkspaceId, Scope.READ) || (granted.data?.resources.length ?? 0) > 0),
		},
	);
	const environments = data?.environments ?? [];

	const requested =
		params.get("env") ?? (activeWorkspaceId !== null ? readStorage(storageKey(activeWorkspaceId)) : null);
	const active: Environment | undefined =
		environments.find((e) => e.name === requested || e.id === requested) ??
		environments.find((e) => e.type === EnvironmentType.PRODUCTION) ??
		environments[0];

	const setActive = (env: Environment) => {
		if (activeWorkspaceId !== null) writeStorage(storageKey(activeWorkspaceId), env.name);
		const next = new URLSearchParams(params);
		next.set("env", env.name);
		setParams(next);
	};

	return {
		environments,
		active,
		requested,
		setActive,
		isLoading: scopes.isLoading || granted.isLoading || environmentsLoading,
	};
}

export function environmentTypeLabel(type: EnvironmentType): string {
	switch (type) {
		case EnvironmentType.PRODUCTION:
			return "production";
		case EnvironmentType.STAGING:
			return "staging";
		case EnvironmentType.UNSPECIFIED:
			return "";
	}
}
