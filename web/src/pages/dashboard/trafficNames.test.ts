import { describe, expect, test } from "bun:test";

import { DELETED_RESOURCE, trafficResourceName } from "./trafficNames";

describe("trafficResourceName", () => {
	const live = new Map([["a", "api"]]);
	const deleted = new Map([["b", "worker"]]);

	test("names a live resource and links it", () => {
		expect(trafficResourceName("a", live, deleted)).toEqual({ name: "api", live: true });
	});

	test("marks a deleted resource with its recorded name", () => {
		expect(trafficResourceName("b", live, deleted)).toEqual({ name: "worker (deleted)", live: false });
	});

	test("falls back when the deleted resource's name is unknown", () => {
		expect(trafficResourceName("c", live, deleted)).toEqual({ name: DELETED_RESOURCE, live: false });
	});
});
