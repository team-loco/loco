import { expect, test } from "bun:test";

import { limitReason, limitUsage, usageLabel } from "./limits";

test("has no usage until the limit loads", () => {
	expect(limitUsage(3, undefined, "workspaces", "This organization has")).toBeNull();
	expect(limitUsage(3, 0, "workspaces", "This organization has")).toBeNull();
});

test("labels the count against the limit", () => {
	const usage = limitUsage(1200, 5000, "members", "This organization has");
	expect(usage && usageLabel(usage)).toBe("1,200 of 5,000 members");
});

test("blocks a create only at or past the limit", () => {
	expect(limitReason(limitUsage(99, 100, "resources", "This workspace has"))).toBeNull();
	expect(limitReason(limitUsage(100, 100, "resources", "This workspace has"))).toStartWith(
		"This workspace has 100 of 100 resources.",
	);
	expect(limitReason(limitUsage(120, 100, "resources", "This workspace has"))).toContain("120 of 100 resources");
	expect(limitReason(null)).toBeNull();
});
