import type { ReactNode } from "react";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "./AlertDialog";

export function ConfirmDialog({
	open,
	title,
	description,
	children,
	confirmLabel,
	pendingLabel,
	pending = false,
	cancelLabel = "Cancel",
	onConfirm,
	onClose,
}: {
	open: boolean;
	title: ReactNode;
	description: ReactNode;
	children?: ReactNode;
	confirmLabel: string;
	pendingLabel?: string;
	pending?: boolean;
	cancelLabel?: string;
	onConfirm: () => void;
	onClose: () => void;
}) {
	return (
		<AlertDialog
			open={open}
			onOpenChange={(next) => {
				if (!next && !pending) onClose();
			}}
		>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>{title}</AlertDialogTitle>
					<AlertDialogDescription>{description}</AlertDialogDescription>
					{children}
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel disabled={pending}>{cancelLabel}</AlertDialogCancel>
					<AlertDialogAction disabled={pending} onClick={onConfirm}>
						{pending ? (pendingLabel ?? confirmLabel) : confirmLabel}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}
