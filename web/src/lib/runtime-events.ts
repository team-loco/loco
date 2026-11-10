import type { Event } from "@gen/loco/event/v1/event_pb";

export type RuntimeEvent = {
	id: string;
	type: string;
	reason: string;
	message: string;
	placementId: string;
	region: string;
	timestamp: Event["createdAt"];
};

export const RUNTIME_EVENT_TYPES = ["resource.degraded", "resource.recovered"];

export function runtimeEvent(event: Event): RuntimeEvent {
	const field = (key: string): string => {
		const value = event.data?.[key];
		return typeof value === "string" ? value : "";
	};
	return {
		id: event.id,
		type: event.type,
		reason: field("reason"),
		message: field("message"),
		placementId: field("placementId"),
		region: field("region"),
		timestamp: event.createdAt,
	};
}
