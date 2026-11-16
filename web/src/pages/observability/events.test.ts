import { expect, test } from "bun:test";
import { eventTitle, severityOf, eventWindow } from "./events";
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

test("relative live ranges include events received between clock ticks", () => {
	const range = { kind: "relative", spanMs: 60000 } as const;
	expect(eventWindow(range, 100000, 100500, true)).toEqual({ from: 40500, to: 100500 });
	expect(eventWindow(range, 100000, 100500, false)).toEqual({ from: 40000, to: 100000 });
	expect(eventWindow({ kind: "absolute", from: 1000, to: 2000 }, 100000, 100500, true)).toEqual({
		from: 1000,
		to: 2000,
	});
});
