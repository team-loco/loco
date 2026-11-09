import { ArrowRight, Check, Copy } from "lucide-react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";

import { COPIED_MS, INSTALL_COMMAND } from "./links";

export function SplashCtas({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	const [copied, copy] = useCopy(COPIED_MS);

	return (
		<div className="flex max-w-full flex-wrap justify-center gap-2.5">
			<Button
				variant="outline"
				title="Copy install command"
				onClick={() => {
					copy("install", INSTALL_COMMAND);
				}}
				className="h-11 max-w-full min-w-0 gap-3.5 rounded-[9px] pr-2 pl-4 font-mono"
			>
				<span className="min-w-0 truncate">
					<span className="text-fg4">$</span> {INSTALL_COMMAND}
				</span>
				<span className="flex size-7 shrink-0 items-center justify-center rounded-[6px] bg-panel text-fg3">
					{copied === null ? <Copy aria-hidden="true" /> : <Check aria-hidden="true" className="text-ok-fg" />}
				</span>
			</Button>
			<Button
				size="xl"
				onClick={onSignIn}
				disabled={redirecting}
				className="h-11 rounded-[9px] px-[18px] text-md font-medium"
			>
				Get started
				<ArrowRight aria-hidden="true" className="size-[15px]" />
			</Button>
		</div>
	);
}
