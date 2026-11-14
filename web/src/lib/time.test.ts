import { expect, test } from "bun:test";

import { shortAgoLabel } from "./time";

test("shortAgoLabel fits minutes, hours and days in a few characters", () => {
	const now = Date.UTC(2026, 0, 10);
	expect(shortAgoLabel(now - 10_000, now)).toBe("now");
	expect(shortAgoLabel(now - 5 * 60_000, now)).toBe("5m ago");
	expect(shortAgoLabel(now - 3 * 3_600_000, now)).toBe("3h ago");
	expect(shortAgoLabel(now - 2 * 86_400_000, now)).toBe("2d ago");
});
