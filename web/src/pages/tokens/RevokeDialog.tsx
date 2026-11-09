import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { revokeToken } from "@gen/loco/token/v1/token-TokenService_connectquery";
import type { Token } from "@gen/loco/token/v1/token_pb";

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
import { maybeTsMs } from "@/lib/time";

import { relAgo } from "./model";

export function RevokeDialog({
	token,
	now,
	onClose,
	onRevoked,
}: {
	token: Token | null;
	now: number;
	onClose: () => void;
	onRevoked: (name: string) => void;
}) {
	const mutation = useMutation(revokeToken);
	const [shown, setShown] = useState<Token | null>(token);
	if (token !== null && token !== shown) setShown(token);
	const used = maybeTsMs(shown?.lastUsedAt);

	const confirm = async () => {
		if (shown === null) return;
		try {
			await mutation.mutateAsync({ name: shown.name, entityType: shown.entityType, entityId: shown.entityId });
			onRevoked(shown.name);
		} catch (err) {
			toastConnectError(err, "Failed to revoke token");
		}
	};

	return (
		<AlertDialog
			open={token !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>Revoke {shown?.name ?? ""}?</AlertDialogTitle>
					<AlertDialogDescription>
						Requests using {shown?.name ?? ""} will fail immediately. This can&apos;t be undone.
					</AlertDialogDescription>
					<span className="text-[12.5px] text-fg3">
						{used === undefined ? "Never used" : `Last used ${relAgo(used, now)}`}
					</span>
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
