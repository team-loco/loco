import { InvitationStatus, type Invitation } from "@gen/loco/org/v1/org_pb";

import type { BadgeTone } from "@/components/design/Badge";

export function statusBadge(status: InvitationStatus): {
	label: string;
	tone: BadgeTone;
} {
	switch (status) {
		case InvitationStatus.EMAILED:
			return { label: "Emailed", tone: "ok" };
		case InvitationStatus.FAILED:
			return { label: "Failed", tone: "bad" };
		case InvitationStatus.NO_TRANSPORT:
			return { label: "Not sent", tone: "neutral" };
		case InvitationStatus.UNSPECIFIED:
			return { label: "Unknown", tone: "muted" };
	}
}

export function statusDetail(invitation: Invitation): string {
	switch (invitation.status) {
		case InvitationStatus.EMAILED:
			return "Emailed";
		case InvitationStatus.FAILED:
			return `Email failed: ${invitation.deliveryError}`;
		case InvitationStatus.NO_TRANSPORT:
			return "No email transport configured";
		case InvitationStatus.UNSPECIFIED:
			return "Unknown";
	}
}
