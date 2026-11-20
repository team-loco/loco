import { useQuery } from "@connectrpc/connect-query";
import { type Environment } from "@gen/loco/environment/v1/environment_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { useQueryState } from "nuqs";
import { useEffect } from "react";
import { useLocation } from "react-router";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { readStorage, writeStorage } from "@/lib/storage";

const storageKey = (workspaceId: string) => `loco:env:v1:${workspaceId}`;

export function useEnvironments() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const scopes = useMyScopes();
	const [envParam, setEnvParam] = useQueryState("env");
	const grants =
		activeOrgId !== null && activeWorkspaceId !== null
			? scopes.resourceGrants(activeOrgId, activeWorkspaceId)
			: undefined;
	// GetScopes does not say which workspace a granted resource is in; only a grant in this
	// workspace lets the user list its environments.
	const listGranted = typeof grants === "object" && grants.length > 0;
	const granted = useTanstackQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: listGranted,
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

	const requested = envParam ?? (activeWorkspaceId !== null ? readStorage(storageKey(activeWorkspaceId)) : null);
	const active: Environment | undefined =
		environments.find((e) => e.slug === requested || e.id === requested) ?? environments[0];

	const setActive = (env: Environment) => {
		if (activeWorkspaceId !== null) writeStorage(storageKey(activeWorkspaceId), env.slug);
		void setEnvParam(env.slug);
	};

	return {
		environments,
		active,
		requested,
		setActive,
		// A disabled query reports loading while another view fetches the same list.
		isLoading: scopes.isLoading || (listGranted && granted.isLoading) || environmentsLoading,
	};
}

/** Writes the active environment into the URL once environments load, so every workspace URL names one. */
export function useEnvironmentInUrl() {
	const { active, isLoading } = useEnvironments();
	const [env, setEnv] = useQueryState("env");
	const name = active?.slug;
	const { pathname } = useLocation();
	useEffect(() => {
		// The browser is already on another path whose page is still loading; that page writes its own env.
		if (pathname !== window.location.pathname) return;
		if (!isLoading && name !== undefined && env !== name) void setEnv(name, { history: "replace" });
	}, [isLoading, name, env, setEnv, pathname]);
}
