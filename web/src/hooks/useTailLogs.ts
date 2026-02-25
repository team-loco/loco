import { useState, useEffect, useRef } from "react";
import { createClient } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@/gen/loco/observability/v1/observability_pb";
import type { LogEntry } from "@/gen/loco/observability/v1/observability_pb";
import type { ClusterTransport } from "@/components/observability/ObsProvider";
import type { ParsedQuery } from "@/lib/obs-query-parser";

const MAX_TAIL_ENTRIES = 500;

interface UseTailLogsOptions {
	clusterTransports: ClusterTransport[];
	workspaceId: string;
	resourceIds: string[];
	parsedQuery: ParsedQuery;
	enabled?: boolean;
}

export function useTailLogs({
	clusterTransports,
	workspaceId,
	resourceIds,
	parsedQuery,
	enabled = true,
}: UseTailLogsOptions) {
	const [entries, setEntries] = useState<LogEntry[]>([]);
	const [isConnected, setIsConnected] = useState(false);
	const abortRef = useRef<AbortController | null>(null);

	useEffect(() => {
		if (!enabled || !workspaceId || clusterTransports.length === 0) {
			setEntries([]);
			setIsConnected(false);
			return;
		}

		// Abort any previous streams
		abortRef.current?.abort();
		const abort = new AbortController();
		abortRef.current = abort;

		setEntries([]);
		setIsConnected(false);

		let connectedCount = 0;

		const streamCluster = async (ct: ClusterTransport) => {
			try {
				const client = createClient(ObservabilityProxyService, ct.transport);
				for await (const msg of client.tailLogs(
					{
						workspaceId,
						resourceIds,
						search: parsedQuery.search,
						levels: parsedQuery.levels,
						labels: parsedQuery.labels,
					},
					{ signal: abort.signal },
				)) {
					if (abort.signal.aborted) break;

					if (msg.event.case === "entry") {
						connectedCount++;
						if (connectedCount === 1) setIsConnected(true);
						setEntries((prev) => {
							const next = [msg.event.value as LogEntry, ...prev];
							return next.length > MAX_TAIL_ENTRIES
								? next.slice(0, MAX_TAIL_ENTRIES)
								: next;
						});
					}
					// heartbeat — no-op, just keeps stream alive
				}
			} catch (err) {
				if (err instanceof Error && err.name === "AbortError") return;
				// Silently eat errors per-cluster; could surface per-cluster error state if needed
			}
		};

		for (const ct of clusterTransports) {
			void streamCluster(ct);
		}

		return () => {
			abort.abort();
			setIsConnected(false);
		};
	}, [
		enabled,
		workspaceId,
		// Use JSON for stable comparison of arrays/objects
		JSON.stringify(resourceIds), // eslint-disable-line react-hooks/exhaustive-deps
		JSON.stringify(parsedQuery), // eslint-disable-line react-hooks/exhaustive-deps
		clusterTransports.map((ct) => ct.cluster.clusterId).join(","), // eslint-disable-line react-hooks/exhaustive-deps
	]);

	return { entries, isConnected };
}
