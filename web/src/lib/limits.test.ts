import { expect, test } from "bun:test";

import { limitPillLabel, limitReason, limitTone, limitUsage, usageLabel } from "./limits";

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

test("explains personal and account caps", () => {
	expect(limitReason(limitUsage(5, 5, "personal tokens", "personal"))).toBe(
		"You've reached the limit of 5 personal tokens. Delete one, or ask a Loco admin to raise it.",
	);
	expect(limitReason(limitUsage(3, 3, "organizations you created", "account"))).toBe(
		"You've reached the limit of 3 organizations you created. Delete one, or ask a Loco admin to raise it.",
	);
});

test("labels a near-limit pill with formatted usage", () => {
	const usage = limitUsage(9000, 10000, "resources", "workspace");
	expect(usage && limitPillLabel(usage)).toBe("9,000 of 10,000 used");
});

test("labels a full pill with its scope", () => {
	for (const [scope, label] of Object.entries({
		organization: "Org limit reached",
		workspace: "Workspace limit reached",
		personal: "Personal limit reached",
		account: "Account limit reached",
		resource: "Resource limit reached",
		"": "Limit reached",
	})) {
		const usage = limitUsage(10, 10, "tokens", scope);
		expect(usage && limitPillLabel(usage)).toBe(label);
	}
});
