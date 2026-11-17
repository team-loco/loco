import type { Event } from "@gen/loco/event/v1/event_pb";

export type RuntimeEvent = {
	id: string;
	seq: string;
	actorType: string;
	actorName: string;
	actorEmail: string;
	subjectType: string;
	requestId: string;
	data: NonNullable<Event["data"]>;
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
	"deployment.ready",
	"deployment.failed",
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
		seq: event.seq.toString(),
		actorType: event.actorType,
		actorName: event.actorName,
		actorEmail: event.actorEmail,
		subjectType: event.subjectType,
		requestId: event.requestId,
		data: event.data ?? {},
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
