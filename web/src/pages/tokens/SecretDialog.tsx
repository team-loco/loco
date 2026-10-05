import { CheckIcon, CopyIcon, KeyRoundIcon, TriangleAlertIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { cn } from "@/lib/utils";

export interface RevealedSecret {
	name: string;
	secret: string;
}

export function SecretDialog({ revealed, onClose }: { revealed: RevealedSecret | null; onClose: () => void }) {
	return (
		<Dialog
			open={revealed !== null}
			disablePointerDismissal
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			{revealed !== null && <SecretBody key={revealed.secret} revealed={revealed} onClose={onClose} />}
		</Dialog>
	);
}

function SecretBody({ revealed, onClose }: { revealed: RevealedSecret; onClose: () => void }) {
	const [copied, setCopied] = useState(false);

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(revealed.secret);
			setCopied(true);
		} catch {
			setCopied(false);
		}
	};

	return (
		<DialogContent title={revealed.name} icon={<KeyRoundIcon />} className="top-[7vh] w-[520px]">
			<DialogBody className="gap-3.5 px-4 py-[18px]">
				<div className="flex items-center gap-2 rounded-lg bg-warn-bg px-3 py-2.5 text-warn-fg">
					<TriangleAlertIcon className="size-[15px] shrink-0" />
					Copy it now. You won&apos;t see it again.
				</div>
				<div className="flex h-[42px] items-center gap-2 rounded-lg border border-line bg-bg2 pr-1.5 pl-3">
					<span className="min-w-0 flex-1 truncate font-mono text-base select-all">{revealed.secret}</span>
					<Button
						variant="outline"
						className={cn("h-[30px] px-2.5 font-medium", copied && "border-ok-fg text-ok-fg hover:border-ok-fg")}
						onClick={() => {
							void copy();
						}}
					>
						{copied ? <CheckIcon className="size-[13px]" /> : <CopyIcon className="size-[13px]" />}
						{copied ? "Copied" : "Copy"}
					</Button>
				</div>
			</DialogBody>
			<DialogFooter>
				<Button variant="inverted" size="lg" onClick={onClose}>
					Done
				</Button>
			</DialogFooter>
		</DialogContent>
	);
}
