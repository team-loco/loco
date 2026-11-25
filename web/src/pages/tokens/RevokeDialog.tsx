import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { revokeToken } from "@gen/loco/token/v1/token-TokenService_connectquery";
import type { Token } from "@gen/loco/token/v1/token_pb";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { toastConnectError } from "@/lib/error-handler";
import { maybeTsMs } from "@/lib/time";

import { relAgo } from "./model";
import { InlineCode } from "@/components/design/InlineCode";

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
		<ConfirmDialog
			open={token !== null}
			title={
				<>
					Revoke <InlineCode>{shown?.name ?? ""}</InlineCode>?
				</>
			}
			description={
				<>
					Requests using <InlineCode>{shown?.name ?? ""}</InlineCode> will fail immediately. This can&apos;t be undone.
				</>
			}
			confirmLabel="Revoke"
			pendingLabel="Revoking…"
			pending={mutation.isPending}
			onConfirm={() => {
				void confirm();
			}}
			onClose={onClose}
		>
			<span className="text-[12.5px] text-fg3">
				{used === undefined ? "Never used" : `Last used ${relAgo(used, now)}`}
			</span>
		</ConfirmDialog>
	);
}
