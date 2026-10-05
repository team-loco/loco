import { ServerIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";

import { imageError } from "./drafts";

export function NewServiceDialog({
	open,
	onOpenChange,
	onAdd,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onAdd: (image: string) => void;
}) {
	const [image, setImage] = useState("");
	const trimmed = image.trim();
	const error = imageError(trimmed);
	const ok = trimmed !== "" && error === null;

	const submit = () => {
		if (!ok) return;
		onAdd(trimmed);
		setImage("");
	};

	return (
		<Dialog
			open={open}
			onOpenChange={(next: boolean) => {
				onOpenChange(next);
				if (!next) setImage("");
			}}
		>
			<DialogContent title="New service" icon={<ServerIcon />} className="w-[480px]">
				<DialogBody className="gap-2.5">
					<Field label="Container image" error={error}>
						<Input
							autoFocus
							value={image}
							placeholder="registry.example.com/team/checkout:1.4.2"
							aria-invalid={error !== null}
							className="h-[38px] px-3 text-md"
							onChange={(e) => {
								setImage(e.target.value);
							}}
							onKeyDown={(e) => {
								if (e.key === "Enter") {
									e.preventDefault();
									submit();
								}
							}}
						/>
					</Field>
				</DialogBody>
				<DialogFooter>
					<Button
						variant="outline"
						onClick={() => {
							onOpenChange(false);
						}}
					>
						Cancel
					</Button>
					<Button disabled={!ok} onClick={submit}>
						Add service
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}
