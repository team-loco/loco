import { describe, expect, test } from "bun:test";

import { markerGroups } from "./metric-markers";

const marker = (t: number, label: string) => ({ t, label, title: label, color: "red" });

describe("deploy markers", () => {
	test("merges deploys closer than 4% of the range under the first label", () => {
		expect(
			markerGroups([marker(30, "api b"), marker(10, "api a"), marker(12, "web a"), marker(90, "web b")], 0, 100),
		).toEqual([
			{ t: 10, label: "api a +1" },
			{ t: 30, label: "api b" },
			{ t: 90, label: "web b" },
		]);
	});
});
