import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampNow } from "@bufbuild/protobuf/wkt";
import { OrganizationSchema } from "@gen/loco/org/v1/org_pb";
import { suspensionReason } from "./suspension";

test("an active organization has no suspension reason", () => {
	expect(suspensionReason(undefined)).toBeNull();
	expect(suspensionReason(create(OrganizationSchema, { id: "org" }))).toBeNull();
});

test("a suspended organization returns the operator's reason", () => {
	const org = create(OrganizationSchema, { suspendedAt: timestampNow(), suspensionReason: "Unpaid invoice" });
	expect(suspensionReason(org)).toBe("Unpaid invoice");
});
