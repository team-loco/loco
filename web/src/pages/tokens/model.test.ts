import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { TokenSchema } from "@gen/loco/token/v1/token_pb";

import { DAY_MS } from "@/lib/time";

import { allowsFor, expiryOptions, expiresLabel, tokenStatus } from "./model";

const NOW = Date.UTC(2026, 10, 8);

function expiringIn(ms: number) {
	return create(TokenSchema, { name: "ci", expiresAt: timestampFromMs(NOW + ms) });
}

describe("token expiry", () => {
	test("warns from seven days out and errors once expired", () => {
		expect(tokenStatus(expiringIn(7 * DAY_MS + 1), NOW)).toBe("active");
		expect(tokenStatus(expiringIn(7 * DAY_MS), NOW)).toBe("expiring");
		expect(tokenStatus(expiringIn(-1), NOW)).toBe("expired");
	});

	test("labels an expired token with how long ago it expired", () => {
		expect(expiresLabel(expiringIn(-3 * DAY_MS), NOW)).toBe("expired 3 days ago");
		expect(expiresLabel(expiringIn(DAY_MS), NOW)).toBe("in 1 day");
	});
});

describe("allows list", () => {
	test("lists each kind up to its highest granted level", () => {
		expect(
			allowsFor([
				{ kind: "res", level: 1 },
				{ kind: "res", level: 2 },
				{ kind: "ws", level: 1 },
			]),
		).toEqual([
			"List resources, environments and members (selected workspaces)",
			"View status, logs, events and deployments (selected resources)",
			"Deploy, scale, restart, edit variables and domains (selected resources)",
		]);
	});
});

describe("token expiry options", () => {
	test("includes the configured maximum and only shorter presets", () => {
		const day = DAY_MS / 1000;
		expect(expiryOptions(BigInt(365 * day))).toEqual([day, 7 * day, 30 * day, 365 * day]);
		expect(expiryOptions(BigInt(2 * day))).toEqual([day, 2 * day]);
		expect(expiryOptions(BigInt(30 * day))).toEqual([day, 7 * day, 30 * day]);
		expect(expiryOptions(90n)).toEqual([90]);
	});
});
