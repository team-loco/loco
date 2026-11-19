import { useState, useEffect, useEffectEvent } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityService, type LogEntry } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";

const MAX_TAIL_ENTRIES = 500;

interface UseTailLogsOptions {
	workspaceId: string;
	environmentId: string;
	allEnvironments?: boolean;
	resourceIds: string[];
	/** A log query; the API parses it. */
	query: string;
	sinceMs?: number | undefined;
	enabled?: boolean;
}

interface TailState {
	key: string;
	entries: LogEntry[];
	/** Arrival time in ms of each entry, newest first. */
	arrivals: number[];
	startedAt: number | null;
	isConnected: boolean;
	error: Error | null;
}

const NO_TAIL: Omit<TailState, "key"> = { entries: [], arrivals: [], startedAt: null, isConnected: false, error: null };

export function tailLogsRequest({
	workspaceId,
	environmentId,
	allEnvironments = false,
	resourceIds,
	query,
	sinceMs,
}: UseTailLogsOptions) {
	return {
		workspaceId,
		environmentId: allEnvironments ? "" : environmentId,
		resourceIds,
		query,
		since: sinceMs === undefined ? undefined : msToTimestamp(sinceMs),
	};
}

export function useTailLogs(options: UseTailLogsOptions) {
	const { workspaceId, environmentId, allEnvironments = false, sinceMs, enabled = true } = options;
	const transport = useTransport();
	const request = tailLogsRequest(options);
	const streamKey = JSON.stringify([
		enabled,
		createConnectQueryKey({ schema: ObservabilityService, transport, cardinality: "finite" }),
		{ ...request, since: sinceMs ?? null },
	]);
	const [tail, setTail] = useState<TailState>({ key: streamKey, ...NO_TAIL });
	if (tail.key !== streamKey) {
		setTail({ key: streamKey, ...NO_TAIL });
	}
	const openStream = useEffectEvent((signal: AbortSignal) => {
		const consume = async () => {
			if (!enabled || workspaceId === "" || (!allEnvironments && environmentId === "")) return;
			try {
				const startedAt = Date.now();
				setTail((previous) => (previous.key === streamKey ? { ...previous, startedAt } : previous));
				const client = createClient(ObservabilityService, transport);
				for await (const message of client.tailLogs(request, { signal })) {
					if (signal.aborted) return;
					const arrivedAt = Date.now();
					setTail((previous) => {
						const same = previous.key === streamKey;
						const entries = same ? previous.entries : [];
						const arrivals = same ? previous.arrivals : [];
						return {
							key: streamKey,
							entries:
								message.event.case === "entry" ? [message.event.value, ...entries].slice(0, MAX_TAIL_ENTRIES) : entries,
							arrivals: message.event.case === "entry" ? [arrivedAt, ...arrivals].slice(0, MAX_TAIL_ENTRIES) : arrivals,
							startedAt: same ? previous.startedAt : startedAt,
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
					arrivals: previous.key === streamKey ? previous.arrivals : [],
					startedAt: previous.key === streamKey ? previous.startedAt : null,
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
		return () => {
			abort.abort();
		};
	}, [streamKey]);

	return tail.key === streamKey ? tail : NO_TAIL;
}
