import { expect, test } from "bun:test";
import { eventTitle, severityOf } from "./events";
test("runtime reasons have service-level titles and severity", () => {
	expect(eventTitle("resource.degraded", "crash_looping")).toBe("Restarting after crash");
	expect(eventTitle("resource.degraded", "health_check_failing")).toBe("Health check failing");
	expect(eventTitle("resource.degraded", "unschedulable")).toBe("Couldn't schedule service");
	expect(eventTitle("resource.recovered", "")).toBe("Service recovered");
	expect(severityOf("resource.degraded", "out_of_memory")).toBe("error");
	expect(severityOf("resource.recovered", "")).toBe("normal");
});
test("rollout, scale, restart and build events read as actions", () => {
	expect(eventTitle("deployment.created", "")).toBe("Rollout started");
	expect(eventTitle("resource.scaled", "")).toBe("Scaled");
	expect(eventTitle("build.failed", "")).toBe("Build failed");
	expect(severityOf("build.failed", "")).toBe("error");
	expect(severityOf("resource.restarted", "")).toBe("normal");
});
