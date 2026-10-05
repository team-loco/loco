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
	AlertDialogTitle,
} from "@/components/design/AlertDialog";
import { toastConnectError } from "@/lib/error-handler";

import { relAgo, tsMillis } from "./model";

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
	const used = shown === null ? null : tsMillis(shown.lastUsedAt);

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
			<AlertDialogContent className="w-[520px]">
				<div className="border-b border-line px-4 py-3.5">
					<AlertDialogTitle>Revoke {shown?.name ?? ""}</AlertDialogTitle>
				</div>
				<AlertDialogDescription className="flex flex-col gap-2 px-4 py-[18px] text-foreground" render={<div />}>
					<span>
						Requests using <span className="font-semibold">{shown?.name ?? ""}</span> will fail immediately.
					</span>
					<span className="text-[12.5px] text-fg3">{used === null ? "Never used" : `Last used ${relAgo(used, now)}`}</span>
				</AlertDialogDescription>
				<AlertDialogFooter>
					<AlertDialogCancel className="px-3">Cancel</AlertDialogCancel>
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
