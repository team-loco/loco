import { expect, test } from "bun:test";
import { eventTitle, severityOf } from "./events";
test("runtime reasons have service-level titles and severity", () => {
	expect(eventTitle("crash_looping")).toBe("Restarting after crash");
	expect(eventTitle("health_check_failing")).toBe("Health check failing");
	expect(eventTitle("unschedulable")).toBe("Couldn't schedule service");
	expect(eventTitle("")).toBe("Service recovered");
	expect(severityOf("resource.degraded", "out_of_memory")).toBe("error");
	expect(severityOf("resource.recovered", "")).toBe("normal");
});
