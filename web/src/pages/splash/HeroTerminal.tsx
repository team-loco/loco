import { AnimatedSpan, Terminal, TypingAnimation } from "@/components/design/Terminal";

const OUTPUT_STEPS = [
	{ delay: 3100, text: "→ Building image from Dockerfile..." },
	{ delay: 3500, text: "→ Pushing to registry..." },
	{ delay: 3900, text: "→ Creating Kubernetes resources..." },
	{ delay: 4300, text: "→ Provisioning SSL certificate..." },
] as const;

export function HeroTerminal() {
	return (
		<Terminal
			className="max-h-none max-w-none rounded-lg border-line bg-bg2 font-mono shadow-popover"
			headerClassName="border-line bg-bg3 px-3 py-2.5"
		>
			<AnimatedSpan delay={0} className="flex gap-2 text-base">
				<span className="text-fg4">$</span>
				<TypingAnimation delay={200} className="text-base text-foreground">
					{"loco init my-api"}
				</TypingAnimation>
			</AnimatedSpan>
			<AnimatedSpan delay={1400} className="text-base text-fg3">
				✓ Created loco.toml in working directory.
			</AnimatedSpan>
			<AnimatedSpan delay={1900} className="text-base opacity-0 select-none">
				{" "}
			</AnimatedSpan>
			<AnimatedSpan delay={2100} className="flex gap-2 text-base">
				<span className="text-fg4">$</span>
				<TypingAnimation delay={2300} className="text-base text-foreground">
					{"loco deploy"}
				</TypingAnimation>
			</AnimatedSpan>
			{OUTPUT_STEPS.map((step) => (
				<AnimatedSpan key={step.text} delay={step.delay} className="text-base text-fg3">
					{step.text}
				</AnimatedSpan>
			))}
			<AnimatedSpan delay={4800} className="text-base text-ok-fg">
				✓ Deployment successful
			</AnimatedSpan>
			<AnimatedSpan delay={5100} className="text-base text-link">
				{"  "}https://my-api.onloco.app
			</AnimatedSpan>
		</Terminal>
	);
}
