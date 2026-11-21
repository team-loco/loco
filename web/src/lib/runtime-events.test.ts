import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";

import { runtimeEvent } from "./runtime-events";

describe("runtimeEvent", () => {
	test("names every region a deployment event lists", () => {
		const event = create(EventSchema, {
			type: "deployment.failed",
			data: { regions: ["us-ca-1", "us-va-1"] },
		});
		expect(runtimeEvent(event).region).toBe("us-ca-1, us-va-1");
	});

	test("keeps the region of a regional event", () => {
		const event = create(EventSchema, { type: "resource.degraded", data: { region: "us-va-1" } });
		expect(runtimeEvent(event).region).toBe("us-va-1");
	});
});
