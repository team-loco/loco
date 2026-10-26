import { describe, expect, test } from "bun:test";

import { resolveAPIURL } from "./api-url";

describe("API address", () => {
	test("uses the current origin when no address is configured", () => {
		expect(resolveAPIURL(undefined, "", "https://loco.example.test")).toBe("https://loco.example.test");
	});

	test("preserves the build-time address without runtime configuration", () => {
		expect(resolveAPIURL(undefined, "https://api.loco.build", "https://loco.example.test")).toBe(
			"https://api.loco.build",
		);
	});

	test("uses the installation address ahead of the published image's address", () => {
		expect(resolveAPIURL("/api", "https://api.loco.build", "https://loco.example.test")).toBe(
			"https://loco.example.test/api",
		);
	});

	test("allows an explicit runtime origin to replace a compiled address", () => {
		expect(
			resolveAPIURL("https://api.region.example.test/", "https://api.loco.build", "https://loco.example.test"),
		).toBe("https://api.region.example.test");
	});

	test("an empty runtime address explicitly selects the current origin", () => {
		expect(resolveAPIURL("", "https://api.loco.build", "https://loco.example.test")).toBe("https://loco.example.test");
	});
});
