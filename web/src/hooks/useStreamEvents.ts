import { getEvents } from "@/gen/app/v1";
import { useQuery } from "@connectrpc/connect-query";

export interface KubernetesEvent {
	timestamp: string;
	severity: "Normal" | "Warning";
	eventType: string;
	pod?: string;
	message: string;
}

export function useStreamEvents(appId: string) {
	const {
		data: eventsRes,
		isLoading,
		error,
	} = useQuery(
		getEvents,
		appId ? { appId: BigInt(appId), limit: 50 } : undefined,
		{ enabled: !!appId }
	);

	const events: KubernetesEvent[] = (eventsRes?.events ?? []).map((event) => ({
		timestamp: event.timestamp
			? new Date(
					Number((event.timestamp as Record<string, unknown>).seconds) * 1000
			  ).toISOString()
			: new Date().toISOString(),
		severity: (event.type === "Warning" ? "Warning" : "Normal") as
			| "Normal"
			| "Warning",
		eventType: event.reason || event.type || "Event",
		pod: event.podName,
		message: event.message || "",
	}));

	return {
		events,
		isLoading,
		error,
	};
}
