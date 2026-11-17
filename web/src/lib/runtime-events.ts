import type { Event } from "@gen/loco/event/v1/event_pb";

export type RuntimeEvent = {
	id: string;
	event: Event;
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
export const ENVIRONMENT_EVENT_TYPES = [
	...RUNTIME_EVENT_TYPES,
	"resource.restarted",
	"deployment.created",
	"resource.scaled",
	"build.created",
	"build.started",
	"build.succeeded",
	"build.failed",
	"build.canceled",
];

export const ROLLOUT_EVENT_TYPES = ["deployment.ready", "deployment.failed"];

export function runtimeEvent(event: Event): RuntimeEvent {
	const field = (key: string): string => {
		const value = event.data?.[key];
		return typeof value === "string" ? value : typeof value === "number" ? String(value) : "";
	};
	return {
		id: event.id,
		event,
		type: event.type,
		reason: field("reason"),
		message: field("message"),
		placementId: field("placementId"),
		region: field("region"),
		environmentId: event.environmentId || field("environmentId"),
		replicas: field("replicas"),
		cpu: field("cpu"),
		memory: field("memory"),
		timestamp: event.createdAt,
	};
}

/** Unknown build origins belong only to unfiltered history. */
export function eventInEnvironment(
	event: Pick<RuntimeEvent, "type" | "environmentId">,
	environmentId: string,
): boolean {
	return (
		event.environmentId === environmentId ||
		(event.environmentId === "" && !ENVIRONMENT_EVENT_TYPES.includes(event.type))
	);
}
