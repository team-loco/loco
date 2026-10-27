import type { Organization } from "@gen/loco/org/v1/org_pb";

export function suspensionReason(org: Organization | undefined): string | null {
	if (org?.suspendedAt === undefined) return null;
	return org.suspensionReason;
}
