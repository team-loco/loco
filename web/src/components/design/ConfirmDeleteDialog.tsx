import { useRef, useState, type ReactNode, type RefObject } from "react";

import {
	AlertDialog,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogTitle,
} from "./AlertDialog";
import { Button } from "./Button";
import { modalTitle } from "./Dialog";
import { Input } from "./Input";
import { InlineCode } from "@/components/design/InlineCode";

export function ConfirmDeleteDialog({
	open,
	onOpenChange,
	title,
	text,
	target,
	confirmLabel,
	pending,
	blocked = false,
	onConfirm,
	children,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: ReactNode;
	text: ReactNode;
	target: string;
	confirmLabel: string;
	pending: boolean;
	blocked?: boolean;
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
						blocked={blocked}
						onConfirm={onConfirm}
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
	blocked,
	onConfirm,
	children,
}: {
	inputRef: RefObject<HTMLInputElement | null>;
	title: ReactNode;
	text: ReactNode;
	target: string;
	confirmLabel: string;
	pending: boolean;
	blocked: boolean;
	onConfirm: () => void;
	children: ReactNode;
}) {
	const [typed, setTyped] = useState("");
	const typedOk = typed.trim() === target && !blocked;

	return (
		<form
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				if (typedOk && !pending) onConfirm();
			}}
		>
			<div className="flex flex-col gap-3.5 px-5 pt-[18px] pb-4">
				<AlertDialogTitle className={modalTitle}>{title}</AlertDialogTitle>
				<AlertDialogDescription className="text-base leading-normal text-foreground">{text}</AlertDialogDescription>
				{children}
				<label className="flex flex-col gap-1.5">
					<span className="text-[12.5px] text-fg2">
						Type <InlineCode>{target}</InlineCode> to confirm
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
			<div className="flex justify-end gap-2 border-t border-line px-5 py-3.5">
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
