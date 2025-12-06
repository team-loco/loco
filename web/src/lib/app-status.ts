import { AppStatus } from "@/gen/app/v1/app_pb";

export function getStatusLabel(status?: number): string {
	if (status === undefined || status === null) return "pending";
	switch (status) {
		case AppStatus.AVAILABLE:
			return "running";
		case AppStatus.PROGRESSING:
			return "deploying";
		case AppStatus.DEGRADED:
			return "degraded";
		case AppStatus.UNAVAILABLE:
			return "unavailable";
		case AppStatus.IDLE:
			return "idle";
		default:
			return "pending";
	}
}
