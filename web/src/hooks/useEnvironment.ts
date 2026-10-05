import { useQuery } from "@connectrpc/connect-query";
import { EnvironmentType, type Environment } from "@gen/loco/environment/v1/environment_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import { useSearchParams } from "react-router";

import { useOrgWorkspace } from "@/context/ContextProvider";

const storageKey = (workspaceId: string) => `loco_env_${workspaceId}`;

function readStored(workspaceId: string): string | null {
	try {
		return localStorage.getItem(storageKey(workspaceId));
	} catch {
		return null;
	}
}

export function useEnvironments() {
	const { activeWorkspaceId } = useOrgWorkspace();
	const [params, setParams] = useSearchParams();
	const { data, isLoading } = useQuery(
		listEnvironments,
		{ workspaceId: activeWorkspaceId ?? "" },
		{ enabled: activeWorkspaceId !== null },
	);
	const environments = data?.environments ?? [];

	const requested = params.get("env") ?? (activeWorkspaceId !== null ? readStored(activeWorkspaceId) : null);
	const active: Environment | undefined =
		environments.find((e) => e.name === requested || e.id === requested) ??
		environments.find((e) => e.type === EnvironmentType.PRODUCTION) ??
		environments[0];

	const setActive = (env: Environment) => {
		if (activeWorkspaceId !== null) {
			try {
				localStorage.setItem(storageKey(activeWorkspaceId), env.name);
			} catch {
				// storage unavailable
			}
		}
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

export function environmentDotClass(type: EnvironmentType): string {
	switch (type) {
		case EnvironmentType.PRODUCTION:
			return "bg-primary";
		case EnvironmentType.STAGING:
			return "bg-warn";
		case EnvironmentType.DEV:
			return "bg-fg4";
		case EnvironmentType.UNSPECIFIED:
			return "bg-fg4";
	}
}
