import { useState, useEffect, useEffectEvent } from "react";
import { createClient } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";
import type { LogEntry } from "@gen/loco/observability/v1/observability_pb";
import type { ClusterTransport } from "@/lib/obs";
import type { ParsedQuery } from "@/lib/obs-query-parser";

const MAX_TAIL_ENTRIES = 500;

interface UseTailLogsOptions {
	clusterTransports: ClusterTransport[];
	workspaceId: string;
	resourceIds: string[];
	parsedQuery: ParsedQuery;
	enabled?: boolean;
}

interface TailState {
	key: string;
	entries: LogEntry[];
	isConnected: boolean;
}

const NO_TAIL: Omit<TailState, "key"> = { entries: [], isConnected: false };

export function useTailLogs({
	clusterTransports,
	workspaceId,
	resourceIds,
	parsedQuery,
	enabled = true,
}: UseTailLogsOptions) {
	const streamKey = JSON.stringify({
		enabled,
		workspaceId,
		resourceIds,
		parsedQuery,
		clusterIds: clusterTransports.map((ct) => ct.cluster.clusterId),
	});
	const [tail, setTail] = useState<TailState>({ key: streamKey, ...NO_TAIL });

	const openStreams = useEffectEvent((signal: AbortSignal) => {
		if (!enabled || workspaceId === "") return;

		const append = (entry: LogEntry) => {
			setTail((prev) => {
				const entries = prev.key === streamKey ? [entry, ...prev.entries] : [entry];
				return { key: streamKey, entries: entries.slice(0, MAX_TAIL_ENTRIES), isConnected: true };
			});
		};

		const streamCluster = async (ct: ClusterTransport) => {
			const client = createClient(ObservabilityProxyService, ct.transport);
			const request = {
				workspaceId,
				resourceIds,
				search: parsedQuery.search,
				levels: parsedQuery.levels,
				labels: parsedQuery.labels,
			};
			try {
				for await (const msg of client.tailLogs(request, { signal })) {
					if (signal.aborted) return;
					if (msg.event.case === "entry") append(msg.event.value);
				}
			} catch {
				return;
			}
		};

		for (const ct of clusterTransports) {
			void streamCluster(ct);
		}
	});

	useEffect(() => {
		const abort = new AbortController();
		openStreams(abort.signal);
		return () => {
			abort.abort();
		};
	}, [streamKey]);

	return tail.key === streamKey ? tail : NO_TAIL;
}
