import { describe, expect, test } from "bun:test";

import { imageName, joinImage, shortImageRef, splitImage } from "./image";

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

test("imageName drops the tag and digest but keeps the registry and path", () => {
	expect(imageName("registry.example.com:5000/team/api:v2.1")).toBe("registry.example.com:5000/team/api");
	expect(imageName(`ghcr.io/team/api@sha256:${"a".repeat(64)}`)).toBe("ghcr.io/team/api");
	expect(imageName("nginx")).toBe("nginx");
});

describe("splitImage and joinImage", () => {
	const digest = `sha256:${"a".repeat(64)}`;
	const forms = [
		"ghcr.io/acme/web",
		"ghcr.io/acme/web:1.4.2",
		`ghcr.io/acme/web@${digest}`,
		`registry.example.com:5000/acme/web:1.4.2@${digest}`,
	];

	test("round-trips every reference form", () => {
		for (const image of forms) {
			const { repository, reference } = splitImage(image);
			expect(joinImage(repository, reference)).toBe(image);
		}
	});

	test("editing the repository keeps the tag and digest", () => {
		for (const image of forms) {
			const { repository, reference } = splitImage(image);
			expect(joinImage("ghcr.io/acme/shop", reference)).toBe(image.replace(repository, "ghcr.io/acme/shop"));
		}
	});
});
