import { useState, useEffect, useCallback } from "react";
import { createClient } from "@connectrpc/connect";
import { ResourceService } from "@/gen/resource/v1/resource_pb";
import type { LogEntry } from "@/gen/resource/v1/resource_pb";
import { createTransport } from "@/auth/connect-transport";

export function useStreamLogs(resourceId: string, tailLimit?: number) {
	const [logs, setLogs] = useState<LogEntry[]>([]);
	const [isLoading, setIsLoading] = useState(true);
	const [error, setError] = useState<Error | null>(null);
	const [refreshKey, setRefreshKey] = useState(0);

	const refetch = useCallback(() => {
		setRefreshKey((prev) => prev + 1);
		setLogs([]);
		setIsLoading(true);
		setError(null);
	}, []);

	useEffect(() => {
		if (!resourceId) {
			setIsLoading(false);
			return;
		}

		let isMounted = true;
		const abortController = new AbortController();

		const streamLogs = async () => {
			try {
				const client = createClient(ResourceService, createTransport());
				const logsList: LogEntry[] = [];
				let isFirstUpdate = true;

				// Stream logs from the server
				for await (const logEntry of client.watchLogs(
					{ resourceId: BigInt(resourceId), limit: tailLimit },
					{ signal: abortController.signal }
				)) {
					if (!isMounted) break;
					logsList.unshift(logEntry);
					if (isMounted) {
						setLogs([...logsList]);
						if (isFirstUpdate) {
							setIsLoading(false);
							isFirstUpdate = false;
						}
					}
				}
			} catch (err) {
				if (!isMounted) return;
				if (err instanceof Error && err.name === "AbortError") {
					return;
				}
				const errorMsg =
					err instanceof Error ? err.message : "Failed to stream logs";
				setError(err instanceof Error ? err : new Error(errorMsg));
				setIsLoading(false);
			}
		};

		streamLogs();

		return () => {
			isMounted = false;
			abortController.abort();
		};
	}, [resourceId, refreshKey, tailLimit]);

	return {
		logs,
		isLoading,
		error,
		refetch,
	};
}
