import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { InvitationSchema, OrgMemberSchema, UserSchema } from "@gen/loco/org/v1/org_pb";

import {
	buildRows,
	inviteFailure,
	levelBlocked,
	levelPlan,
	parseEmails,
	roleOf,
	untilLabel,
	type Member,
	type ScopeName,
} from "./model";

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

function member(id: string, name: string, email: string): Member {
	const user = create(UserSchema, { id, name, email });
	return { member: create(OrgMemberSchema, { user, scopes: ["member"] }), workspaces: [] };
}

function invitation(id: string, email: string, createdMs: number) {
	return create(InvitationSchema, { id, email, createdAt: timestampFromMs(createdMs) });
}

function holding(...held: ScopeName[]) {
	return (s: ScopeName) => held.includes(s);
}

describe("parseEmails", () => {
	test("splits on commas, spaces and new lines and drops duplicates", () => {
		expect(parseEmails("ana@acme.dev, lchen@acme.dev\nANA@acme.dev  bob@x")).toEqual([
			{ value: "ana@acme.dev", valid: true },
			{ value: "lchen@acme.dev", valid: true },
			{ value: "bob@x", valid: true },
		]);
	});

	test("marks entries that are not a single local@domain", () => {
		expect(parseEmails("ana, @acme.dev, a@b@c, ana@").map((c) => c.valid)).toEqual([false, false, false, false]);
	});
});

describe("roleOf", () => {
	test("is Admin only with the admin scope", () => {
		expect(roleOf(["member", "read", "write", "admin"])).toBe("Admin");
		expect(roleOf(["member", "read", "write"])).toBe("Member");
	});
});

describe("levelPlan", () => {
	test("raising a level grants only the missing scopes", () => {
		expect(levelPlan(["read"], 3)).toEqual({ revokeAll: false, grant: ["write", "admin"] });
	});

	test("lowering a level revokes everything and grants the lower set", () => {
		expect(levelPlan(["read", "write", "admin"], 1)).toEqual({ revokeAll: true, grant: ["read"] });
		expect(levelPlan(["read"], 0)).toEqual({ revokeAll: true, grant: [] });
	});

	test("picking the current level does nothing", () => {
		expect(levelPlan(["admin"], 3)).toBeNull();
	});
});

describe("levelBlocked", () => {
	test("lowering needs admin on the workspace", () => {
		expect(levelBlocked(["read", "write"], 1, holding("read", "write"))).not.toBeNull();
		expect(levelBlocked(["read", "write"], 1, holding("read", "write", "admin"))).toBeNull();
	});

	test("granting needs write and every scope granted", () => {
		expect(levelBlocked([], 1, holding("read"))).not.toBeNull();
		expect(levelBlocked([], 3, holding("read", "write"))).toBe("You don't hold admin on this workspace");
		expect(levelBlocked([], 2, holding("read", "write"))).toBeNull();
	});
});

describe("inviteFailure", () => {
	test("separates an existing member or invitation from other failures", () => {
		const exists = new ConnectError("that email already has an open invitation", Code.AlreadyExists);
		expect(inviteFailure("a@b", exists).kind).toBe("exists");
		expect(inviteFailure("a@b", new ConnectError("bad", Code.InvalidArgument))).toEqual({
			kind: "failed",
			email: "a@b",
			message: "Bad.",
		});
	});
});

describe("untilLabel", () => {
	test("counts hours under a day and days after", () => {
		const now = 1_000 * DAY_MS;
		expect(untilLabel(now + 5 * HOUR_MS, now)).toBe("in 5h");
		expect(untilLabel(now + 6 * DAY_MS, now)).toBe("in 6 days");
		expect(untilLabel(now - 1, now)).toBe("expired");
	});
});

describe("buildRows", () => {
	const members = [member("2", "Zoe", "zoe@x"), member("1", "", "me@x"), member("3", "Ann", "ann@x")];
	const invitations = [invitation("i1", "old@x", 1), invitation("i2", "new@x", 2)];

	test("lists newest invitations first, then you, then members by name", () => {
		const rows = buildRows(members, invitations, "all", "", "1");
		expect(rows.map((r) => (r.kind === "invitation" ? r.invitation.id : r.member.member.user?.id))).toEqual([
			"i2",
			"i1",
			"1",
			"3",
			"2",
		]);
	});

	test("filters by view and by name or email", () => {
		expect(buildRows(members, invitations, "invitations", "", "1")).toHaveLength(2);
		expect(buildRows(members, invitations, "members", "ZO", "1")).toHaveLength(1);
		expect(buildRows(members, invitations, "all", "new@", "1")).toHaveLength(1);
	});
});
