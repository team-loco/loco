import { ArrowDown, Check, Copy } from "lucide-react";
import { Fragment } from "react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

import { INSTALL_COMMAND } from "./links";

const INSTALL_SEGMENTS = INSTALL_COMMAND.split("/");
import { CornerMarks, Frame } from "./primitives";

export function Hero() {
	const [copied, copy] = useCopy(1500);
	const isCopied = copied !== null;

	return (
		<section className="relative">
			<Frame className="flex flex-col items-center gap-6 bg-[linear-gradient(var(--bg3)_1px,transparent_1px),linear-gradient(90deg,var(--bg3)_1px,transparent_1px)] bg-size-[48px_48px] bg-position-[-1px_-1px] pt-[104px] pb-14 text-center">
				<CornerMarks />
				<a
					href="#access"
					className="flex h-7 items-center gap-2 rounded-full border border-line bg-background pr-1 pl-3 text-[13px] text-fg2 transition-colors hover:border-line2 hover:text-foreground"
				>
					Invite-only beta
					<span className="flex h-5 items-center rounded-full bg-bg3 px-2 text-sm font-medium text-primary">
						Request access
					</span>
				</a>
				<h1 className="m-0 max-w-[880px] text-[clamp(46px,6.6vw,80px)] leading-[1.02] font-semibold tracking-[-0.035em] text-balance">
					Deploy containers,
					<br />
					not YAML.
				</h1>
				<p className="m-0 max-w-[560px] text-[18.5px] leading-[1.55] text-pretty text-fg2">
					loco turns a Dockerfile into a live HTTPS service on Kubernetes. One command, real infrastructure,
					nothing to babysit.
				</p>
				<div className="mt-1 flex flex-wrap justify-center gap-2.5">
					<Button size="xl" nativeButton={false} render={<a href="#access" />} className="h-11 px-5 text-lg font-medium">
						Request access
					</Button>
					<Button
						variant="outline"
						size="xl"
						nativeButton={false}
						render={<a href="#start" />}
						className="h-11 px-[18px] text-lg font-medium hover:border-foreground"
					>
						How it works
						<ArrowDown className="size-[15px]" />
					</Button>
				</div>
				<Button
					variant="ghost"
					title="Copy"
					onClick={() => {
						copy("install", INSTALL_COMMAND);
					}}
					className="h-auto min-h-8 max-w-full gap-2.5 rounded-[7px] px-3 py-1.5 font-mono text-[13px] whitespace-normal text-fg3 hover:text-foreground"
				>
					<span className="text-primary">$</span>
					<span className="min-w-0 text-left">
						{INSTALL_SEGMENTS.map((segment, i) => (
							<Fragment key={segment}>
								{i > 0 && (
									<>
										/<wbr />
									</>
								)}
								{segment}
							</Fragment>
						))}
					</span>
					<span className={cn("flex", isCopied ? "text-ok-fg" : "text-fg4")}>
						{isCopied ? <Check className="size-[13px]" /> : <Copy className="size-[13px]" />}
					</span>
				</Button>
			</Frame>
		</section>
	);
}
