import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { DeploymentPhase, DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";

import { maybeTsMs } from "@/lib/time";

export interface DeploymentWatch {
	phase: DeploymentPhase | undefined;
	message: string;
	at: number;
}

const IDLE: DeploymentWatch = { phase: undefined, message: "", at: 0 };
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
					const at = maybeTsMs(ev.timestamp) ?? Date.now();
					setStates((prev) => ({ ...prev, [deploymentId]: { phase: ev.status, message: ev.message, at } }));
					void queryClient.invalidateQueries();
					if (ev.status === DeploymentPhase.RUNNING) break;
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
