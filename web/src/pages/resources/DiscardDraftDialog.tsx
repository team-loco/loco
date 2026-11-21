import { toast } from "sonner";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";

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
			title={`Discard ${name}?`}
			description="The draft is removed from this browser. This can't be undone."
			confirmLabel="Discard draft"
			onConfirm={() => {
				onDiscard();
				toast.success(`Discarded ${name}`);
			}}
			onClose={onClose}
		/>
	);
}
