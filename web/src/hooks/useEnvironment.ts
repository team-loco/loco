import { useQuery } from "@connectrpc/connect-query";
import { EnvironmentType, type Environment } from "@gen/loco/environment/v1/environment_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { useSearchParams } from "react-router";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { readStorage, writeStorage } from "@/lib/storage";

const storageKey = (workspaceId: string) => `loco:env:v1:${workspaceId}`;

export function useEnvironments() {
	const { activeWorkspaceId } = useOrgWorkspace();
	const [params, setParams] = useSearchParams();
	const { data, isLoading } = useQuery(
		listEnvironments,
		{ workspaceId: activeWorkspaceId ?? "" },
		{ enabled: activeWorkspaceId !== null },
	);
	const environments = data?.environments ?? [];

	const requested = params.get("env") ?? (activeWorkspaceId !== null ? readStorage(storageKey(activeWorkspaceId)) : null);
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

	return { environments, active, setActive, isLoading };
}

export function environmentTypeLabel(type: EnvironmentType): string {
	switch (type) {
		case EnvironmentType.PRODUCTION:
			return "production";
		case EnvironmentType.STAGING:
			return "staging";
		case EnvironmentType.DEV:
			return "dev";
		case EnvironmentType.UNSPECIFIED:
			return "";
	}
}
