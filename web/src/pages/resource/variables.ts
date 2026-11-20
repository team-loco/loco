import { createConnectQueryKey } from "@connectrpc/connect-query";
import type { QueryClient } from "@tanstack/react-query";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import { isEnvKey } from "@/lib/dotenv";

import { isDeployed, type RegionView } from "./model";

/** Why name cannot be added as a new variable, given the names already in use. */
export function variableNameProblem(name: string, taken: ReadonlySet<string>): string | undefined {
	if (name === "") return "Enter a name.";
	if (!isEnvKey(name)) return "Use letters, digits and underscores, not starting with a digit.";
	if (taken.has(name)) return `${name} is already a variable.`;
	return undefined;
}

function variablesKey(deployment: Deployment): string {
	const env = Object.entries(getServiceSpec(deployment)?.env ?? {}).toSorted(([a], [b]) => a.localeCompare(b));
	return JSON.stringify(env);
}

/** The deployed regions when their deployments run different variables, else none. */
export function regionsWithDifferentVariables(regions: RegionView[]): string[] {
	const deployed = regions
		.filter(isDeployed)
		.flatMap((r) => (r.current === undefined ? [] : [{ name: r.name, key: variablesKey(r.current) }]));
	return new Set(deployed.map((r) => r.key)).size > 1 ? deployed.map((r) => r.name).toSorted() : [];
}

/** Refetches the variables of one resource in one environment. */
export async function refreshVariables(client: QueryClient, resourceId: string, environmentId: string): Promise<void> {
	await client.invalidateQueries({
		queryKey: createConnectQueryKey({
			schema: getResourceEnv,
			input: { resourceId, environmentId },
			cardinality: "finite",
		}),
	});
}
