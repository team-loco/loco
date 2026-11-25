import { EyeIcon, EyeOffIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";

const MASK = "•";

/** The value as shown: the secret itself when revealed, bullets of the same length otherwise. */
export function maskSecret(secret: string, shown: boolean): string {
	return shown ? secret : MASK.repeat(secret.length);
}

/** A one-time secret, masked until revealed. The copy button always copies the full secret. */
export function SecretValue({
	secret,
	shown,
	onShownChange,
	noun,
	toastText,
}: {
	secret: string;
	shown: boolean;
	onShownChange: (shown: boolean) => void;
	/** What the secret is called in button labels, such as "token". */
	noun: string;
	toastText?: string;
}) {
	const eyeLabel = `${shown ? "Hide" : "Show"} ${noun}`;
	return (
		<div className="flex items-center gap-2.5 rounded-lg bg-panel py-2.5 pr-2.5 pl-3 font-mono text-[12.5px] break-all">
			{shown ? (
				<span data-secret-value="" className="min-w-0 flex-1 select-all [overflow-wrap:anywhere]">
					{secret}
				</span>
			) : (
				<span
					data-secret-value=""
					role="img"
					aria-label={`Hidden ${noun}`}
					className="min-w-0 flex-1 select-none [overflow-wrap:anywhere]"
				>
					<span aria-hidden="true">{maskSecret(secret, false)}</span>
				</span>
			)}
			<Button
				type="button"
				variant="outline"
				size="icon-sm"
				className="shrink-0"
				aria-pressed={shown}
				aria-label={eyeLabel}
				title={eyeLabel}
				onClick={() => {
					onShownChange(!shown);
				}}
			>
				{shown ? <EyeOffIcon /> : <EyeIcon />}
			</Button>
			<CopyButton text={secret} name={`Copy ${noun}`} {...(toastText === undefined ? {} : { toastText })} />
		</div>
	);
}
