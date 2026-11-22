import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { EventService } from "@gen/loco/event/v1/event_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

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
 * Streams the environment's deployment events from now on. Each one refetches the deployment lists on screen,
 * and the first one turns `heard` on, so the shell starts reading deployments only once one changes.
 */
export function useDeployEvents(orgId: string | null, workspaceId: string | null, environmentId: string | undefined) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const scope = `${orgId ?? ""}/${workspaceId ?? ""}/${environmentId ?? ""}`;
	const [heard, setHeard] = useState("");
	useEffect(() => {
		if (orgId === null || workspaceId === null || environmentId === undefined) return;
		const abort = new AbortController();
		const client = createClient(EventService, transport);
		const startTime = msToTimestamp(Date.now());
		let retry: ReturnType<typeof setTimeout> | undefined;
		const consume = async () => {
			try {
				for await (const message of client.streamEvents(
					{ orgId, workspaceId, environmentId, startTime, query: eventTypesQuery(DEPLOY_EVENT_TYPES) },
					{ signal: abort.signal },
				)) {
					if (message.events.length === 0) continue;
					setHeard(scope);
					void queryClient.invalidateQueries({
						queryKey: createConnectQueryKey({ schema: listDeployments, cardinality: "finite" }),
					});
				}
			} catch (error) {
				const refused =
					error instanceof ConnectError &&
					(error.code === Code.PermissionDenied || error.code === Code.Unauthenticated);
				if (abort.signal.aborted || refused) return;
			}
			if (!abort.signal.aborted) retry = setTimeout(() => void consume(), RETRY_MS);
		};
		void consume();
		return () => {
			abort.abort();
			clearTimeout(retry);
		};
	}, [orgId, workspaceId, environmentId, scope, transport, queryClient]);
	return heard === scope;
}
