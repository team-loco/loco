import { useState, useEffect } from "react";
import { createClient } from "@connectrpc/connect";
import { ResourceService } from "@/gen/resource/v1/resource_pb";
import type { LogEntry } from "@/gen/resource/v1/resource_pb";
import { createTransport } from "@/auth/connect-transport";

export function useStreamLogs(appId: string) {
	const [logs, setLogs] = useState<LogEntry[]>([]);
	const [isLoading, setIsLoading] = useState(true);
	const [error, setError] = useState<Error | null>(null);

	useEffect(() => {
		if (!appId) {
			setIsLoading(false);
			return;
		}

		let isMounted = true;
		const abortController = new AbortController();

		const streamLogs = async () => {
			try {
				const client = createClient(ResourceService, createTransport());
				const logsList: LogEntry[] = [];

				// Stream logs from the server
				for await (const logEntry of client.streamLogs(
					{ resourceId: BigInt(appId) },
					{ signal: abortController.signal }
				)) {
					if (!isMounted) break;
					logsList.push(logEntry);
					if (isMounted) {
						setLogs([...logsList]);
					}
				}

				if (isMounted) {
					setIsLoading(false);
				}
			} catch (err) {
				if (!isMounted) return;
				if (err instanceof Error && err.name === "AbortError") {
					return;
				}
				const errorMsg = err instanceof Error ? err.message : "Failed to stream logs";
				setError(err instanceof Error ? err : new Error(errorMsg));
				setIsLoading(false);
			}
		};

		streamLogs();

		return () => {
			isMounted = false;
			abortController.abort();
		};
	}, [appId]);

	return {
		logs,
		isLoading,
		error,
	};
}
