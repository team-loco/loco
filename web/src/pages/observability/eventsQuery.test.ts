import { expect, test } from "bun:test";

import { eventsQuery } from "./eventsQuery";
import { quoteValue } from "./query";

const chip = (key: string, value: string, neg = false) => ({ key, value: quoteValue(value), neg });

test("ORs chips of one key, ANDs keys, and keeps negations", () => {
	expect(
		eventsQuery([chip("type", "build.failed"), chip("type", "deployment.failed"), chip("region", "us-va-1", true)], ""),
	).toBe("(@type:build.failed OR @type:deployment.failed) -@region:us-va-1");
});

test("reads the system actor as an actor type and sends quoted chip values once", () => {
	expect(eventsQuery([chip("actor", "system"), chip("reason", "image pull: failed")], "")).toBe(
		'@actor_type:system @reason:"image pull: failed"',
	);
	expect(eventsQuery([chip("reason", 'say "hi" \\ (now)')], "")).toBe(String.raw`@reason:"say \"hi\" \\ (now)"`);
	expect(eventsQuery([{ key: "reason", value: "out_of*", neg: true }], "")).toBe("-@reason:out_of*");
});

test("groups text after chips and passes text alone through", () => {
	expect(eventsQuery([chip("severity", "error")], "oom OR crash")).toBe("@severity:error (oom OR crash)");
	expect(eventsQuery([], " degraded in ")).toBe("degraded in");
});
