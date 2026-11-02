import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { InvitationSchema, InvitationStatus } from "@gen/loco/org/v1/org_pb";
import { statusBadge, statusDetail } from "./invitations";

test("an expired invitation reads as expired", () => {
	expect(statusBadge(InvitationStatus.EXPIRED)).toEqual({ label: "Expired", tone: "warn" });
	const invitation = create(InvitationSchema, { status: InvitationStatus.EXPIRED });
	expect(statusDetail(invitation)).toBe("Expired");
});
