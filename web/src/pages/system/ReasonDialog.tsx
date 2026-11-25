import { useState, type ReactNode } from "react";

import { Button, type ButtonVariant } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";

const MAX_REASON = 500;

function ReasonForm({
	description,
	reasonHint,
	confirmLabel,
	confirmVariant,
	canSubmit,
	pending,
	onSubmit,
	onClose,
	children,
}: {
	description: ReactNode;
	reasonHint: string | undefined;
	confirmLabel: string;
	confirmVariant: ButtonVariant;
	canSubmit: boolean;
	pending: boolean;
	onSubmit: (reason: string) => void;
	onClose: () => void;
	children: ReactNode;
}) {
	const [reason, setReason] = useState("");
	const trimmed = reason.trim();
	const ready = trimmed !== "" && canSubmit && !pending;
	return (
		<form
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				if (ready) onSubmit(trimmed);
			}}
		>
			<DialogBody>
				<div className="text-[13.5px] text-fg2">{description}</div>
				{children}
				<Field label="Reason" hint={reasonHint ?? "One line. It goes in the organization's audit log."}>
					<Input
						value={reason}
						maxLength={MAX_REASON}
						onChange={(e) => {
							setReason(e.target.value);
						}}
					/>
				</Field>
			</DialogBody>
			<DialogFooter>
				<Button type="button" variant="outline" disabled={pending} onClick={onClose}>
					Cancel
				</Button>
				<Button type="submit" variant={confirmVariant} disabled={!ready}>
					{confirmLabel}
				</Button>
			</DialogFooter>
		</form>
	);
}

/** A dialog that takes a one-line reason before it runs `onSubmit`; `children` holds any other fields. */
export function ReasonDialog({
	open,
	title,
	description,
	reasonHint,
	confirmLabel,
	confirmVariant = "default",
	canSubmit = true,
	pending,
	onSubmit,
	onClose,
	children,
}: {
	open: boolean;
	title: string;
	description: ReactNode;
	reasonHint?: string;
	confirmLabel: string;
	confirmVariant?: ButtonVariant;
	canSubmit?: boolean;
	pending: boolean;
	onSubmit: (reason: string) => void;
	onClose: () => void;
	children?: ReactNode;
}) {
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && !pending) onClose();
			}}
		>
			<DialogContent size="form" title={title}>
				<ReasonForm
					description={description}
					reasonHint={reasonHint}
					confirmLabel={confirmLabel}
					confirmVariant={confirmVariant}
					canSubmit={canSubmit}
					pending={pending}
					onSubmit={onSubmit}
					onClose={onClose}
				>
					{children}
				</ReasonForm>
			</DialogContent>
		</Dialog>
	);
}
