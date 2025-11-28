import { useState, useEffect } from "react";

export interface LogEntry {
	timestamp: string;
	level: "INFO" | "WARN" | "ERROR" | "DEBUG";
	message: string;
	pod?: string;
}

export function useStreamLogs(appId: string) {
	const [logs, setLogs] = useState<LogEntry[]>([]);
	const [isLoading, setIsLoading] = useState(true);
	const [error, setError] = useState<Error | null>(null);

	useEffect(() => {
		if (!appId) {
			setIsLoading(false);
			return;
		}

		// TODO: In Phase 3, implement actual WebSocket/SSE streaming
		// For now, return mock data to establish structure
		const mockLogs: LogEntry[] = [
			{
				timestamp: new Date().toISOString(),
				level: "INFO",
				message: "Application started successfully",
				pod: "pod-1",
			},
			{
				timestamp: new Date(Date.now() - 1000).toISOString(),
				level: "INFO",
				message: "Server listening on port 8080",
				pod: "pod-1",
			},
		];

		setLogs(mockLogs);
		setIsLoading(false);
	}, [appId]);

	return {
		logs,
		isLoading,
		error,
	};
}
