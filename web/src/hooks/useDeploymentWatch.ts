import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { getDeployment, listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentService, type Activity, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { getErrorMessage } from "@/lib/error-handler";
import { deploymentEnded, mergeActivity } from "./deployment-watch";

export interface DeploymentWatch {
	deployment: Deployment | undefined;
	activity: Activity[];
	connection: "live" | "reconnecting" | "ended";
	error: string | undefined;
}

const IDLE: DeploymentWatch = { deployment: undefined, activity: [], connection: "live", error: undefined };
const ID_SEPARATOR = ",";

export function useDeploymentWatch(deploymentIds: readonly string[]): (id: string | undefined) => DeploymentWatch {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const cursorsRef = useRef(new Map<string, bigint>());
	const [states, setStates] = useState<Record<string, DeploymentWatch>>({});
	const key = deploymentIds.join(ID_SEPARATOR);
	useEffect(() => {
		if (key === "") return;
		const controller = new AbortController();
		const isAborted = () => controller.signal.aborted;
		const client = createClient(DeploymentService, transport);
		const watch = async (deploymentId: string) => {
			let backoff = 1000;
			while (!isAborted()) {
				try {
					let terminal = false;
					for await (const ev of client.watchDeployment(
						{ deploymentId, afterSeq: cursorsRef.current.get(deploymentId) ?? 0n },
						{ signal: controller.signal },
					)) {
						if (isAborted()) return;
						const deployment = ev.deployment;
						if (deployment === undefined) throw new Error("Deployment watch returned no deployment");
						const last = ev.activity.at(-1)?.seq;
						if (last !== undefined && last > (cursorsRef.current.get(deploymentId) ?? 0n))
							cursorsRef.current.set(deploymentId, last);
						terminal = deploymentEnded(deployment);
						setStates((prev) => ({
							...prev,
							[deploymentId]: {
								deployment,
								activity: mergeActivity(prev[deploymentId]?.activity ?? [], ev.activity),
								connection: "live",
								error: undefined,
							},
						}));
						void queryClient.invalidateQueries({
							queryKey: createConnectQueryKey({
								schema: getDeployment,
								input: { deploymentId },
								transport,
								cardinality: "finite",
							}),
						});
						void queryClient.invalidateQueries({
							queryKey: createConnectQueryKey({
								schema: listDeployments,
								input: { resourceId: deployment.resourceId },
								transport,
								cardinality: undefined,
							}),
						});
						backoff = 1000;
					}
					if (terminal) {
						setStates((prev) => ({
							...prev,
							[deploymentId]: { ...(prev[deploymentId] ?? IDLE), connection: "ended" },
						}));
						return;
					}
					throw new Error("Deployment stream disconnected");
				} catch (error) {
					if (isAborted()) return;
					setStates((prev) => ({
						...prev,
						[deploymentId]: {
							...(prev[deploymentId] ?? IDLE),
							connection: "reconnecting",
							error: getErrorMessage(error, "Deployment stream disconnected"),
						},
					}));
				}
				await reconnectDelay(controller.signal, backoff);
				backoff = Math.min(backoff * 2, 30000);
			}
		};
		for (const id of key.split(ID_SEPARATOR)) void watch(id);
		return () => {
			controller.abort();
		};
	}, [key, transport, queryClient]);
	return (id) => (id === undefined ? IDLE : (states[id] ?? IDLE));
}

async function reconnectDelay(signal: AbortSignal, delay: number): Promise<void> {
	await new Promise<void>((resolve) => {
		const done = () => {
			clearTimeout(timer);
			signal.removeEventListener("abort", done);
			resolve();
		};
		const timer = setTimeout(done, delay);
		signal.addEventListener("abort", done, { once: true });
	});
}
