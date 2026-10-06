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
	lines: readonly (readonly [string, Tone])[];
}

const STEPS: readonly Step[] = [
	{
		n: "01",
		title: "Install the CLI",
		body: "A single Go binary. Completions for bash and zsh included.",
		lines: [
			[`$ ${INSTALL_COMMAND}`, "ink"],
			["$ loco completion zsh > _loco", "ink"],
		],
	},
	{
		n: "02",
		title: "Sign in with GitHub",
		body: "Device flow, no passwords. Your session is scoped to your org and workspaces.",
		lines: [
			["$ loco login", "ink"],
			["! one-time code  8F2A-91C3", "warn"],
			["  open github.com/login/device", "muted"],
			["✓ signed in as nikumar1206", "ok"],
		],
	},
	{
		n: "03",
		title: "Deploy",
		body: "Build, push, roll out, health-check and route. Then it's live.",
		lines: [
			["$ loco deploy api", "ink"],
			["✓ built  sha-8a03f2", "muted"],
			["✓ 3/3 replicas healthy", "muted"],
			["→ https://api.onloco.app", "accent"],
		],
	},
];

export function GetStarted() {
	return (
		<section id="start" className="scroll-mt-[60px] border-t border-line">
			<Frame className="flex flex-col gap-14 py-28">
				<CornerMarks />
				<SectionHeading index="01" eyebrow="Get started" title="Three commands to production." />
				<div className="grid border-t border-line md:grid-cols-3">
					{STEPS.map((step) => (
						<div
							key={step.n}
							className="flex min-w-0 flex-col gap-4 pt-7 pb-2 max-md:not-last:border-b max-md:not-last:border-line max-md:not-last:pb-7 md:not-first:pl-6 md:not-last:border-r md:not-last:border-line md:not-last:pr-6"
						>
							<span className="flex items-baseline gap-3">
								<span className="font-mono text-[12.5px] text-fg4">{step.n}</span>
								<span className="text-[17px] font-semibold">{step.title}</span>
							</span>
							<span className="text-pretty text-fg2 md:min-h-12">{step.body}</span>
							<div className="rounded-[10px] border border-line bg-bg2 px-4 py-3.5 font-mono text-[12.5px] leading-[1.75]">
								{step.lines.map(([text, tone]) => (
									<div key={text} className={`wrap-anywhere whitespace-pre-wrap ${TONE_CLASS[tone]}`}>
										{text}
									</div>
								))}
							</div>
						</div>
					))}
				</div>
			</Frame>
		</section>
	);
}
