import { Check, Copy } from "lucide-react";
import { useRef, useState } from "react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

import { INSTALL_COMMAND } from "./links";
import { CornerMarks, Frame, SectionHeading } from "./primitives";

type Tone = "ink" | "muted" | "warn" | "ok" | "accent";

const TONE_CLASS: Record<Tone, string> = {
	ink: "text-foreground",
	muted: "text-fg3",
	warn: "text-warn",
	ok: "text-ok-fg",
	accent: "text-primary",
};

interface Step {
	n: string;
	title: string;
	body: string;
	command: string;
	output: readonly (readonly [string, Tone])[];
}

const STEPS: readonly Step[] = [
	{
		n: "01",
		title: "Install the CLI",
		body: "Loco is a single Go binary. Requires Go 1.27 or newer.",
		command: INSTALL_COMMAND,
		output: [],
	},
	{
		n: "02",
		title: "Sign in with GitHub",
		body: "Device flow, no passwords. Your session is scoped to your org and workspaces.",
		command: "loco login",
		output: [
			["! one-time code  8F2A-91C3", "warn"],
			["  open github.com/login/device", "muted"],
			["✓ signed in as nikumar1206", "ok"],
		],
	},
	{
		n: "03",
		title: "Deploy",
		body: "Build, push, roll out, health-check and route. Then it's live.",
		command: "loco deploy api",
		output: [
			["✓ built  sha-8a03f2", "muted"],
			["✓ 3/3 replicas healthy", "muted"],
			["→ https://api.onloco.app", "accent"],
		],
	},
];

const STEP_LINES = STEPS.map((step, i) => {
	const before = STEPS.slice(0, i).reduce((sum, s) => sum + 1 + s.output.length, 0);
	const lines: readonly (readonly [string, Tone])[] = [[`$ ${step.command}`, "ink"], ...step.output];
	return lines.map(([text, tone], j) => ({ text, tone, order: before + j }));
});

const TOTAL_LINES = STEP_LINES.flat().length;
const REVEAL_INTERVAL_MS = 260;

function TerminalLine({ text, tone, shown }: { text: string; tone: Tone; shown: boolean }) {
	return (
		<div
			className={cn(
				"wrap-anywhere whitespace-pre-wrap transition-opacity duration-[260ms]",
				TONE_CLASS[tone],
				!shown && "opacity-[0.08] motion-reduce:opacity-100"
			)}
		>
			{text}
		</div>
	);
}

export function GetStarted() {
	const [shown, setShown] = useState(0);
	const [copied, copy] = useCopy(1400);
	const played = useRef(false);

	const revealRef = (el: HTMLElement | null) => {
		if (el === null || played.current) return;
		const observer = new IntersectionObserver(
			(entries) => {
				if (played.current || !entries.some((e) => e.isIntersecting)) return;
				played.current = true;
				observer.disconnect();
				const reveal = (count: number) => {
					setShown(count);
					if (count < TOTAL_LINES) setTimeout(reveal, REVEAL_INTERVAL_MS, count + 1);
				};
				reveal(1);
			},
			{ threshold: 0.35 }
		);
		observer.observe(el);
		return () => {
			observer.disconnect();
		};
	};

	return (
		<section id="start" ref={revealRef} className="scroll-mt-16 border-t border-line">
			<Frame className="flex flex-col gap-14 py-28">
				<CornerMarks />
				<SectionHeading index="01" eyebrow="Get started" title="Three commands to production." />
				<div className="grid border-t border-line md:grid-cols-3">
					{STEPS.map((step, i) => {
						const isCopied = copied === step.n;
						return (
							<div
								key={step.n}
								className="flex min-w-0 flex-col gap-4 pt-7 pb-2 max-md:not-last:border-b max-md:not-last:border-line max-md:not-last:pb-7 md:not-first:pl-6 md:not-last:border-r md:not-last:border-line md:not-last:pr-6"
							>
								<span className="flex items-baseline gap-3">
									<span className="font-mono text-[12.5px] text-fg4">{step.n}</span>
									<span className="text-[17px] font-semibold">{step.title}</span>
								</span>
								<span className="text-pretty text-fg2 md:min-h-12">{step.body}</span>
								<div className="relative rounded-[10px] border border-line bg-bg2 py-3.5 pr-11 pl-4 font-mono text-[12.5px] leading-[1.75]">
									<Button
										variant="ghost"
										size="icon-sm"
										title="Copy command"
										aria-label={`Copy ${step.command}`}
										onClick={() => {
											copy(step.n, step.command);
										}}
										className={cn(
											"absolute top-2 right-2 hover:bg-line hover:text-foreground",
											isCopied ? "text-ok-fg" : "text-fg4"
										)}
									>
										{isCopied ? <Check /> : <Copy />}
									</Button>
									{STEP_LINES[i]?.map((line) => (
										<TerminalLine key={line.order} text={line.text} tone={line.tone} shown={line.order < shown} />
									))}
								</div>
							</div>
						);
					})}
				</div>
			</Frame>
		</section>
	);
}
