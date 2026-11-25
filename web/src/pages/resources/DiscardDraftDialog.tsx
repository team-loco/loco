import { toast } from "@/lib/toast";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { InlineCode, withCode } from "@/components/design/InlineCode";

export function DiscardDraftDialog({
	name,
	open,
	onDiscard,
	onClose,
}: {
	name: string;
	open: boolean;
	onDiscard: () => void;
	onClose: () => void;
}) {
	return (
		<ConfirmDialog
			open={open}
			title={
				<>
					Discard <InlineCode>{name}</InlineCode>?
				</>
			}
			description="The draft is removed from this browser. This can't be undone."
			confirmLabel="Discard draft"
			onConfirm={() => {
				onDiscard();
				toast.success(withCode`Discarded ${name}`);
			}}
			onClose={onClose}
		/>
	);
}
