import { expect, test } from "bun:test";
import { eventTitle, severityOf } from "./events";
test("runtime reasons determine severity", () => {
	expect(severityOf("resource.degraded", "out_of_memory")).toBe("error");
	expect(severityOf("resource.recovered", "")).toBe("normal");
});
test("build failures have error severity", () => {
	expect(severityOf("build.failed", "")).toBe("error");
	expect(severityOf("resource.restarted", "")).toBe("normal");
});

test("unknown event types and reasons remain readable", () => {
	expect(eventTitle("custom.event", "")).toBe("custom.event");
	expect(eventTitle("resource.degraded", "custom_reason")).toBe("custom reason");
});
