import { ArrowRight, Check, Copy } from "lucide-react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";

import { DeployDemo } from "./DeployDemo";
import { INSTALL_COMMAND } from "./links";

const COPIED_MS = 1400;

export function Hero({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	const [copied, copy] = useCopy(COPIED_MS);

	return (
		<section className="relative pt-14 sm:pt-24">
			<div
				aria-hidden="true"
				className="absolute inset-x-0 top-0 bottom-[30%] -z-10 bg-[radial-gradient(color-mix(in_oklch,var(--fg)_9%,transparent)_1px,transparent_1px)] bg-size-[22px_22px] bg-top mask-[linear-gradient(#000_50%,transparent)]"
			/>
			<div className="mx-auto flex w-full max-w-[1160px] flex-col items-center gap-[22px] px-4 pb-16 text-center sm:px-7">
				<h1 className="m-0 text-[clamp(44px,6.4vw,78px)] leading-[1.02] font-medium tracking-[-0.045em] text-balance">
					From Dockerfile
					<br />
					to production.
				</h1>
				<p className="m-0 max-w-[540px] text-[18px] text-pretty text-fg3">
					Run <span className="font-mono text-[16px] text-foreground">loco deploy</span> and your container is live with
					HTTPS, rolling deploys, logs and metrics.
				</p>
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
			</div>
			<div className="mx-auto w-full max-w-[1160px] px-4 pb-28 sm:px-7">
				<DeployDemo />
			</div>
		</section>
	);
}
