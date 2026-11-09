import { useMutation } from "@connectrpc/connect-query";
import { revokeInvitation } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Invitation } from "@gen/loco/org/v1/org_pb";
import { useState } from "react";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/design/AlertDialog";
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
		<AlertDialog
			open={invitation !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>Revoke the invitation to {email}?</AlertDialogTitle>
					<AlertDialogDescription>
						Signing in with {email} won&apos;t add them to the organization until you invite them again.
					</AlertDialogDescription>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel>Cancel</AlertDialogCancel>
					<AlertDialogAction
						disabled={mutation.isPending}
						onClick={() => {
							void confirm();
						}}
					>
						{mutation.isPending ? "Revoking…" : "Revoke"}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}
