import { XIcon } from "lucide-react";
import { useRef, useState, type ReactNode, type RefObject } from "react";

import {
	AlertDialog,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogTitle,
} from "./AlertDialog";
import { Button } from "./Button";
import { Input } from "./Input";

export function ConfirmDeleteDialog({
	open,
	onOpenChange,
	title,
	text,
	target,
	confirmLabel,
	pending,
	onConfirm,
	children,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: string;
	text: string;
	target: string;
	confirmLabel: string;
	pending: boolean;
	onConfirm: () => void;
	children?: ReactNode;
}) {
	const inputRef = useRef<HTMLInputElement>(null);
	return (
		<AlertDialog
			open={open}
			onOpenChange={(next) => {
				if (!pending) onOpenChange(next);
			}}
		>
			<AlertDialogContent className="flex w-[480px] flex-col gap-0 p-0" initialFocus={inputRef}>
				{open && (
					<ConfirmBody
						inputRef={inputRef}
						title={title}
						text={text}
						target={target}
						confirmLabel={confirmLabel}
						pending={pending}
						onConfirm={onConfirm}
						onClose={() => {
							onOpenChange(false);
						}}
					>
						{children}
					</ConfirmBody>
				)}
			</AlertDialogContent>
		</AlertDialog>
	);
}

function ConfirmBody({
	inputRef,
	title,
	text,
	target,
	confirmLabel,
	pending,
	onConfirm,
	onClose,
	children,
}: {
	inputRef: RefObject<HTMLInputElement | null>;
	title: string;
	text: string;
	target: string;
	confirmLabel: string;
	pending: boolean;
	onConfirm: () => void;
	onClose: () => void;
	children: ReactNode;
}) {
	const [typed, setTyped] = useState("");
	const typedOk = typed.trim() === target;

	return (
		<form
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				if (typedOk && !pending) onConfirm();
			}}
		>
			<div className="flex items-center gap-2 border-b border-line px-4 py-3.5">
				<AlertDialogTitle className="flex-1 text-lg font-semibold">{title}</AlertDialogTitle>
				<Button
					type="button"
					variant="ghost"
					size="icon-sm"
					aria-label="Close"
					className="text-fg3 hover:text-foreground"
					disabled={pending}
					onClick={onClose}
				>
					<XIcon className="size-4" />
				</Button>
			</div>
			<div className="flex flex-col gap-3.5 p-4">
				<AlertDialogDescription className="text-base leading-normal text-foreground">{text}</AlertDialogDescription>
				{children}
				<label className="flex flex-col gap-1.5">
					<span className="text-[12.5px] text-fg2">
						Type <span className="font-semibold text-foreground">{target}</span> to confirm
					</span>
					<Input
						ref={inputRef}
						autoComplete="off"
						value={typed}
						aria-invalid={typed !== "" && !typedOk}
						className="h-9 text-[13.5px]"
						disabled={pending}
						onChange={(e) => {
							setTyped(e.target.value);
						}}
					/>
				</label>
			</div>
			<div className="flex justify-end gap-2 border-t border-line px-4 py-3">
				<AlertDialogCancel className="px-3" disabled={pending}>
					Cancel
				</AlertDialogCancel>
				<Button type="submit" variant="destructive" size="lg" disabled={!typedOk || pending}>
					{pending ? "Deleting…" : confirmLabel}
				</Button>
			</div>
		</form>
	);
}
