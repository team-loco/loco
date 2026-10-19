import { describe, expect, test } from "bun:test";

import { shortImageRef } from "./image";

describe("shortImageRef", () => {
	test("keeps short image names and human-readable tags", () => {
		expect(shortImageRef("registry.example.com/team/api:v2.1")).toBe("api:v2.1");
		expect(shortImageRef("registry.example.com:5000/team/api")).toBe("api");
	});

	test("compacts deployment identifiers while retaining their distinguishing suffix", () => {
		const repository = "recommendation-0199d1c2-0000-7000-8000-000000000001";
		const tag = "0199d1c2-0000-7000-8000-000000000002";
		expect(shortImageRef(`registry.example.com/${repository}:${tag}`)).toBe(
			"recommendation-01…000001:0199d1c2-…000002",
		);
	});

	test("compacts digest references independently of the repository length", () => {
		const image = `registry.example.com/recommendation-0199d1c2-0000-7000-8000-000000000001@sha256:${"a".repeat(64)}`;
		expect(shortImageRef(image)).toBe("recommendation-01…000001@sha256:aaaaaaaaaaaa");
	});
});
