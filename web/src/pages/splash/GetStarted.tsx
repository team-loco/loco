import { Check, Copy } from "lucide-react";

import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

import { ENDPOINT, TONE_CLASS, type LineTone } from "./deploy";
import { COPIED_MS, INSTALL_COMMAND } from "./links";
import { Section, SectionTitle } from "./primitives";

interface Step {
	n: string;
	title: string;
	body: string;
	command: string;
	output: readonly (readonly [string, LineTone])[];
}

const STEPS: readonly Step[] = [
	{
		n: "01",
		title: "Install the CLI",
		body: "One binary for macOS and Linux.",
		command: INSTALL_COMMAND,
		output: [
			["Downloading loco (darwin, arm64)", "output"],
			["Installed loco to ~/.local/bin/loco", "ok"],
		],
	},
	{
		n: "02",
		title: "Sign in",
		body: "Opens your browser. Use --device on a machine without one.",
		command: "loco login",
		output: [
			["Opening your browser to sign in. If it doesn't open, visit:", "output"],
			["✔ Authentication successful!", "ok"],
			["  Organization: acme", "output"],
		],
	},
	{
		n: "03",
		title: "Deploy",
		body: "Builds your Dockerfile, rolls out the replicas and serves the service over HTTPS.",
		command: "loco deploy",
		output: [
			["Packed 214 files from . (1.8 MiB compressed)", "output"],
			["Building api from Dockerfile in .", "output"],
			[`api: https://${ENDPOINT}`, "link"],
		],
	},
];

export function GetStarted() {
	const [copied, copy] = useCopy(COPIED_MS);

	return (
		<Section id="start">
			<SectionTitle>Three commands to production.</SectionTitle>
			<div className="grid gap-4 min-[900px]:grid-cols-3">
				{STEPS.map((step) => {
					const isCopied = copied === step.n;
					return (
						<div key={step.n} className="flex min-w-0 flex-col gap-2">
							<span className="font-mono text-xs text-fg4">{step.n}</span>
							<span className="text-[16px] font-medium">{step.title}</span>
							<span className="text-md text-pretty text-fg3 min-[900px]:min-h-10">{step.body}</span>
							<div className="relative mt-1.5 flex-1 rounded-card border border-line bg-panel py-3.5 pr-10 pl-4 font-mono text-[12.5px] leading-[1.85] min-[900px]:min-h-[118px]">
								<Button
									variant="ghost"
									size="icon-sm"
									title="Copy command"
									aria-label={`Copy ${step.command}`}
									onClick={() => {
										copy(step.n, step.command);
									}}
									className={cn(
										"absolute top-2 right-2 hover:bg-panel2 hover:text-foreground",
										isCopied ? "text-ok-fg" : "text-fg4",
									)}
								>
									{isCopied ? <Check /> : <Copy />}
								</Button>
								<div className={cn("truncate", TONE_CLASS.command)}>$ {step.command}</div>
								{step.output.map(([text, tone]) => (
									<div key={text} className={cn("truncate whitespace-pre", TONE_CLASS[tone])}>
										{text}
									</div>
								))}
							</div>
						</div>
					);
				})}
			</div>
		</Section>
	);
}
