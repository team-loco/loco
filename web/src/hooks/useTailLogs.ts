import { useState, useEffect, useEffectEvent } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityProxyService, type LogEntry } from "@gen/loco/observability/v1/observability_pb";
import type { ParsedQuery } from "@/lib/obs-query-parser";

const MAX_TAIL_ENTRIES = 500;

interface UseTailLogsOptions {
	workspaceId: string;
	resourceIds: string[];
	parsedQuery: ParsedQuery;
	enabled?: boolean;
}

interface TailState {
	key: string;
	entries: LogEntry[];
	isConnected: boolean;
	error: Error | null;
}

const NO_TAIL: Omit<TailState, "key"> = { entries: [], isConnected: false, error: null };

export function useTailLogs({ workspaceId, resourceIds, parsedQuery, enabled = true }: UseTailLogsOptions) {
	const transport = useTransport();
	const request = {
		workspaceId,
		resourceIds,
		search: parsedQuery.search,
		levels: parsedQuery.levels,
		labels: parsedQuery.labels,
		regions: parsedQuery.regions ?? [],
		excludedRegions: parsedQuery.excludedRegions ?? [],
	};
	const streamKey = JSON.stringify([
		enabled,
		createConnectQueryKey({ schema: ObservabilityProxyService, transport, cardinality: "finite" }),
		request,
	]);
	const [tail, setTail] = useState<TailState>({ key: streamKey, ...NO_TAIL });
	const openStream = useEffectEvent((signal: AbortSignal) => {
		const consume = async () => {
			if (!enabled || workspaceId === "") return;
			try {
				const client = createClient(ObservabilityProxyService, transport);
				for await (const message of client.tailLogs(request, { signal })) {
					if (signal.aborted) return;
					setTail((previous) => {
						const entries = previous.key === streamKey ? previous.entries : [];
						return {
							key: streamKey,
							entries: message.event.case === "entry"
								? [message.event.value, ...entries].slice(0, MAX_TAIL_ENTRIES)
								: entries,
							isConnected: true,
							error: null,
						};
					});
				}
				if (!signal.aborted) setTail((previous) => ({ ...previous, isConnected: false }));
			} catch (error) {
				if (signal.aborted) return;
				setTail((previous) => ({
					key: streamKey,
					entries: previous.key === streamKey ? previous.entries : [],
					isConnected: false,
					error: error instanceof Error ? error : new Error(String(error)),
				}));
			}
		};
		void consume();
	});

	useEffect(() => {
		const abort = new AbortController();
		openStream(abort.signal);
		return () => { abort.abort(); };
	}, [streamKey]);

	return tail.key === streamKey ? tail : NO_TAIL;
}
