import type { Event } from "@gen/loco/event/v1/event_pb";

export type RuntimeEvent = {
	id: string;
	type: string;
	reason: string;
	message: string;
	placementId: string;
	region: string;
	environmentId: string;
	replicas: string;
	cpu: string;
	memory: string;
	timestamp: Event["createdAt"];
};

export const RUNTIME_EVENT_TYPES = ["resource.degraded", "resource.recovered"];

/** Types the API records with an environment, so ListEvents can filter them by one. */
export const ENVIRONMENT_EVENT_TYPES = [...RUNTIME_EVENT_TYPES, "resource.restarted"];

/** Types recorded without an environment: deploys and scaling name it in their data, builds have none. */
export const ROLLOUT_EVENT_TYPES = [
	"deployment.created",
	"resource.scaled",
	"build.created",
	"build.started",
	"build.succeeded",
	"build.failed",
	"build.canceled",
];

export function runtimeEvent(event: Event): RuntimeEvent {
	const field = (key: string): string => {
		const value = event.data?.[key];
		return typeof value === "string" ? value : typeof value === "number" ? String(value) : "";
	};
	return {
		id: event.id,
		type: event.type,
		reason: field("reason"),
		message: field("message"),
		placementId: field("placementId"),
		region: field("region"),
		environmentId: field("environmentId"),
		replicas: field("replicas"),
		cpu: field("cpu"),
		memory: field("memory"),
		timestamp: event.createdAt,
	};
}
