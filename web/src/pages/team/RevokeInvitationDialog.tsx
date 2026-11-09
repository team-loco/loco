import { useMutation } from "@connectrpc/connect-query";
import { revokeInvitation } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { useState } from "react";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { toastConnectError } from "@/lib/error-handler";

export function RevokeInvitationDialog({
	invitation,
	onClose,
	onRevoked,
}: {
	invitation: Invitation | null;
	onClose: () => void;
	onRevoked: (email: string) => void;
}) {
	const mutation = useMutation(revokeInvitation);
	const [shown, setShown] = useState<Invitation | null>(invitation);
	if (invitation !== null && invitation !== shown) setShown(invitation);
	const email = shown?.email ?? "";

	const confirm = async () => {
		if (shown === null) return;
		try {
			await mutation.mutateAsync({ id: shown.id });
			onRevoked(email);
		} catch (err) {
			toastConnectError(err, "Failed to revoke the invitation");
		}
	};

	return (
		<ConfirmDialog
			open={invitation !== null}
			title={`Revoke the invitation to ${email}?`}
			description={`Signing in with ${email} won't add them to the organization until you invite them again.`}
			confirmLabel="Revoke"
			pendingLabel="Revoking…"
			pending={mutation.isPending}
			onConfirm={() => {
				void confirm();
			}}
			onClose={onClose}
		/>
	);
}
