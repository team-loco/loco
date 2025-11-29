import { useState, useEffect } from "react";

export interface KubernetesEvent {
	timestamp: string;
	severity: "Normal" | "Warning";
	eventType: string;
	pod?: string;
	message: string;
}

export function useStreamEvents(appId: string) {
	const [events, setEvents] = useState<KubernetesEvent[]>([]);
	const [isLoading, setIsLoading] = useState(true);
	const [error, setError] = useState<Error | null>(null);

	useEffect(() => {
		if (!appId) {
			setIsLoading(false);
			return;
		}

		// TODO: In Phase 3, implement actual event streaming
		// For now, return mock data to establish structure
		const mockEvents: KubernetesEvent[] = [
			{
				timestamp: new Date().toISOString(),
				severity: "Normal",
				eventType: "Created",
				pod: "pod-1",
				message: "Pod created successfully",
			},
			{
				timestamp: new Date(Date.now() - 2000).toISOString(),
				severity: "Normal",
				eventType: "Started",
				pod: "pod-1",
				message: "Pod started",
			},
		];

		setEvents(mockEvents);
		setIsLoading(false);
	}, [appId]);

	return {
		events,
		isLoading,
		error,
	};
}
