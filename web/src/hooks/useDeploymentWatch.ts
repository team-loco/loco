import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { DeploymentService, type DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { isInFlight } from "@/lib/deployment-utils";
import { maybeTsMs } from "@/lib/time";

export interface DeploymentWatch {
	phase: DeploymentPhase | undefined;
	message: string;
	updatedMs: number | undefined;
}

const IDLE: DeploymentWatch = { phase: undefined, message: "", updatedMs: undefined };
const ID_SEPARATOR = ",";

export function useDeploymentWatch(deploymentIds: readonly string[]): (id: string | undefined) => DeploymentWatch {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [states, setStates] = useState<Record<string, DeploymentWatch>>({});
	const key = deploymentIds.join(ID_SEPARATOR);
	useEffect(() => {
		if (key === "") return;
		const controller = new AbortController();
		const client = createClient(DeploymentService, transport);
		const watch = async (deploymentId: string) => {
			try {
				for await (const ev of client.watchDeployment({ deploymentId }, { signal: controller.signal })) {
					setStates((prev) => ({
						...prev,
						[deploymentId]: { phase: ev.status, message: ev.message, updatedMs: maybeTsMs(ev.timestamp) },
					}));
					void queryClient.invalidateQueries();
					if (!isInFlight(ev.status)) break;
				}
			} catch {
				return;
			}
		};
		for (const id of key.split(ID_SEPARATOR)) void watch(id);
		return () => {
			controller.abort();
		};
	}, [key, transport, queryClient]);
	return (id) => (id === undefined ? IDLE : (states[id] ?? IDLE));
}
