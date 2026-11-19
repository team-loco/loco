import { expect, test } from "bun:test";
import { SessionKind } from "@gen/loco/user/v1/user_pb";

import { describeSession } from "./sessions";

test("names a CLI session by its host, system and version", () => {
	expect(
		describeSession({
			kind: SessionKind.CLI,
			userAgent: "loco/v1.2.3 (darwin arm64; build-box)",
			ipAddress: "203.0.113.9",
		}),
	).toEqual({ label: "Loco CLI on build-box", detail: "macOS arm64 · v1.2.3", ipAddress: "203.0.113.9" });
});

test("falls back to the browser and system for a web session", () => {
	const chrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X) Chrome/135.0.0.0";
	expect(describeSession({ kind: SessionKind.WEB, userAgent: chrome, ipAddress: "" }).label).toBe("Chrome on macOS");
	expect(describeSession({ kind: SessionKind.CLI, userAgent: "connect-go/1", ipAddress: "" }).label).toBe("Loco CLI");
});
