import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { ConfirmDeleteDialog } from "@/components/design/ConfirmDeleteDialog";

export function DeleteResourceDialog({
	open,
	onOpenChange,
	resource,
	confirmLabel,
	pending,
	onConfirm,
	asQuestion = true,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	resource: Resource;
	confirmLabel: string;
	pending: boolean;
	onConfirm: () => void;
	/** Ends the title with a question mark. */
	asQuestion?: boolean;
}) {
	const running = resource.environmentStatuses.length > 0 ? "It is running. " : "";
	return (
		<ConfirmDeleteDialog
			open={open}
			onOpenChange={onOpenChange}
			title={`Delete ${resource.name}${asQuestion ? "?" : ""}`}
			text={`${running}Deleting it stops its compute right away and releases its domains in every environment. Its logs, metrics and traces may stay queryable for a while. This can't be undone.`}
			target={resource.name}
			confirmLabel={confirmLabel}
			pending={pending}
			onConfirm={onConfirm}
		>
			{resource.partial !== undefined && (
				<p className="rounded-md border border-warn/30 bg-warn-bg px-3 py-2 text-sm text-warn-fg">
					Deleting here causes drift with <code className="font-mono">loco.yaml</code>. Applying the file again can
					recreate this resource. Remove it from the file to keep it deleted.
				</p>
			)}
		</ConfirmDeleteDialog>
	);
}
