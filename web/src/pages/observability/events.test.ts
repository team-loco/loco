import { expect, test } from "bun:test";

import { eventTitle } from "./events";

test("known event reasons read as plain titles", () => {
	expect(eventTitle("BackOff")).toBe("Restarting after crash");
	expect(eventTitle("Unhealthy")).toBe("Health check failing");
	expect(eventTitle("FailedScheduling")).toBe("Couldn't schedule replica");
});

test("unknown reasons are split into words instead of shown as identifiers", () => {
	expect(eventTitle("NetworkNotReady")).toBe("Network not ready");
	expect(eventTitle("")).toBe("Event");
});
