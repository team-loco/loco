import { expect, test } from "bun:test";

import { limitReason, limitTone, limitUsage, usageLabel } from "./limits";

test("has no usage until the limit loads", () => {
	expect(limitUsage(3, undefined, "workspaces", "organization")).toBeNull();
	expect(limitUsage(3, 0, "workspaces", "organization")).toBeNull();
});

test("labels the count against the limit", () => {
	const usage = limitUsage(1200, 5000, "members", "organization");
	expect(usage && usageLabel(usage)).toBe("1,200 / 5,000");
});

test("tones the usage amber from 90% and red at the limit", () => {
	expect(limitTone(limitUsage(89, 100, "resources", "workspace"))).toBeNull();
	expect(limitTone(limitUsage(90, 100, "resources", "workspace"))).toBe("warn");
	expect(limitTone(limitUsage(100, 100, "resources", "workspace"))).toBe("bad");
	expect(limitTone(null)).toBeNull();
});

test("blocks a create only at or past the limit", () => {
	expect(limitReason(limitUsage(99, 100, "resources", "workspace"))).toBeNull();
	expect(limitReason(limitUsage(100, 100, "resources", "workspace"))).toBe(
		"You've reached the limit of 100 resources in this workspace. Delete one, or ask a Loco admin to raise it.",
	);
	expect(limitReason(null)).toBeNull();
});

test("personal and creator limits omit a containing scope", () => {
	expect(limitReason(limitUsage(5, 5, "personal tokens", ""))).toBe(
		"You've reached the limit of 5 personal tokens. Delete one, or ask a Loco admin to raise it.",
	);
	expect(limitReason(limitUsage(3, 3, "organizations you created", ""))).toBe(
		"You've reached the limit of 3 organizations you created. Delete one, or ask a Loco admin to raise it.",
	);
});
