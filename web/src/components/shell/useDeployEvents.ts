import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { EventService, type Event } from "@gen/loco/event/v1/event_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useRef, useState } from "react";

import { eventTypesQuery } from "@/lib/audit-query";
import { msToTimestamp } from "@/lib/time";

const DEPLOY_EVENT_TYPES = [
	"deployment.created",
	"deployment.ready",
	"deployment.failed",
	"deployment.crashed",
	"deployment.canceled",
	"resource.degraded",
];
const RETRY_MS = 15_000;

/**
 * Streams the environment's deployment events from now on while the tab is visible, and hands each batch to
 * `onEvents`. Each batch refetches the deployment lists on screen and turns `heard` on, so the shell starts
 * reading deployments only once one changes; a tab that becomes visible again refetches them once to catch up.
 * `startedMsRef` holds when the shell started watching the current environment.
 */
export function useDeployEvents(
	orgId: string | null,
	workspaceId: string | null,
	environmentId: string | undefined,
	onEvents: (events: readonly Event[]) => void,
) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const scope = `${orgId ?? ""}/${workspaceId ?? ""}/${environmentId ?? ""}`;
	const [heard, setHeard] = useState("");
	const startedMsRef = useRef(0);
	const handle = useEffectEvent(onEvents);
	useEffect(() => {
		if (orgId === null || workspaceId === null || environmentId === undefined) return;
		const client = createClient(EventService, transport);
		const refetch = () => {
			setHeard(scope);
			void queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({ schema: listDeployments, cardinality: "finite" }),
			});
		};
		let abort: AbortController | null = null;
		let retry: ReturnType<typeof setTimeout> | undefined;
		const consume = async (signal: AbortSignal, fromMs: number) => {
			try {
				for await (const message of client.streamEvents(
					{
						orgId,
						workspaceId,
						environmentId,
						startTime: msToTimestamp(fromMs),
						query: eventTypesQuery(DEPLOY_EVENT_TYPES),
					},
					{ signal },
				)) {
					if (message.events.length === 0) continue;
					handle(message.events);
					refetch();
				}
			} catch (error) {
				if (error instanceof ConnectError && [Code.PermissionDenied, Code.Unauthenticated].includes(error.code)) return;
			}
			if (!signal.aborted) retry = setTimeout(() => void consume(signal, fromMs), RETRY_MS);
		};
		const open = (fromMs: number) => {
			abort = new AbortController();
			void consume(abort.signal, fromMs);
		};
		const close = () => {
			abort?.abort();
			abort = null;
			clearTimeout(retry);
		};
		// A hidden tab holds no stream; on return it streams from then on and refetches what changed meanwhile.
		const onVisibility = () => {
			if (document.hidden) {
				close();
				return;
			}
			if (abort !== null) return;
			open(Date.now());
			refetch();
		};
		startedMsRef.current = Date.now();
		if (!document.hidden) open(startedMsRef.current);
		document.addEventListener("visibilitychange", onVisibility);
		return () => {
			close();
			document.removeEventListener("visibilitychange", onVisibility);
		};
	}, [orgId, workspaceId, environmentId, scope, transport, queryClient]);
	return { heard: heard === scope, startedMsRef };
}
