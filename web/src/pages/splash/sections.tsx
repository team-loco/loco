import { FileCode2, MessageSquare } from "lucide-react";
import { lazy, Suspense, type ReactNode } from "react";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";

import { FEATURES, GITHUB_ISSUES_URL, GITHUB_URL, LOCO_TOML_EXAMPLE, OPEN_STANDARDS } from "./content";
import { GitHubIcon } from "./GitHubIcon";

const CodeBlock = lazy(async () => ({ default: (await import("@/components/design/CodeBlock")).CodeBlock }));

function CodeBlockFallback({ filename, children }: { filename: string; children: string }) {
	return (
		<div className="overflow-hidden rounded-lg border border-line bg-background">
			<div className="flex h-[37px] items-center border-b border-line bg-bg2 pr-2 pl-4">
				<span className="font-mono text-sm font-medium text-fg2">{filename}</span>
			</div>
			<pre className="m-0 overflow-auto p-4 font-mono text-sm leading-[1.6] text-(--sh-identifier)">
				<code className="font-mono">{children}</code>
			</pre>
		</div>
	);
}

function Eyebrow({ children }: { children: ReactNode }) {
	return <p className="m-0 mb-2 text-xs font-semibold tracking-[0.08em] text-fg3 uppercase">{children}</p>;
}

function SectionHeading({ eyebrow, title, children }: { eyebrow: string; title: string; children: ReactNode }) {
	return (
		<div className="mx-auto mb-10 max-w-xl text-center">
			<Eyebrow>{eyebrow}</Eyebrow>
			<h2 className="m-0 mb-3 text-[32px] leading-tight font-semibold tracking-[-0.02em]">{title}</h2>
			<p className="m-0 text-md leading-relaxed text-fg2">{children}</p>
		</div>
	);
}

function Tile({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-2 rounded-lg border border-line bg-background p-5">
			<span className="mb-1 flex size-8 items-center justify-center rounded-sm border border-line bg-bg2 text-fg2 [&_svg]:size-4">
				{icon}
			</span>
			<h3 className="m-0 text-md font-semibold">{title}</h3>
			<div className="leading-relaxed text-fg3">{children}</div>
		</div>
	);
}

function InlineCode({ children }: { children: ReactNode }) {
	return <code className="rounded-xs border border-line bg-bg3 px-1.5 py-px font-mono text-sm text-foreground">{children}</code>;
}

function TextLink({ href, children }: { href: string; children: ReactNode }) {
	return (
		<a href={href} target="_blank" rel="noreferrer" className="mt-1 font-medium text-link hover:underline">
			{children}
		</a>
	);
}

export function FeaturesSection() {
	return (
		<section id="features" className="border-y border-line bg-bg2 px-4 py-16 md:px-8">
			<div className="mx-auto max-w-[1200px]">
				<SectionHeading eyebrow="Features" title="Everything You Need">
					Ship with confidence. Modern infrastructure without the complexity.
				</SectionHeading>
				<div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
					{FEATURES.map(({ icon: Icon, title, desc }) => (
						<Tile key={title} icon={<Icon />} title={title}>
							{desc}
						</Tile>
					))}
				</div>
			</div>
		</section>
	);
}

export function ConfigSection() {
	return (
		<section className="px-4 py-16 md:px-8">
			<div className="mx-auto grid max-w-[1000px] items-center gap-12 lg:grid-cols-2">
				<div className="flex flex-col gap-3">
					<Eyebrow>Simple Configuration</Eyebrow>
					<h2 className="m-0 mb-2 text-[32px] leading-tight font-semibold tracking-[-0.02em]">
						One File.
						<br />
						Full Control.
					</h2>
					<p className="m-0 text-md leading-relaxed text-fg2">
						A <InlineCode>loco.toml</InlineCode> is all you need. No Kubernetes manifests, no Helm charts to
						debug—just clean, readable config with sensible defaults.
					</p>
					<p className="m-0 text-md leading-relaxed text-fg2">
						Run <InlineCode>loco init</InlineCode> to generate a starter file, then customise from there. You can
						also configure everything through the UI.
					</p>
				</div>
				<Suspense fallback={<CodeBlockFallback filename="loco.toml">{LOCO_TOML_EXAMPLE}</CodeBlockFallback>}>
					<CodeBlock filename="loco.toml" language="toml">
						{LOCO_TOML_EXAMPLE}
					</CodeBlock>
				</Suspense>
			</div>
		</section>
	);
}

export function OpenSourceSection() {
	return (
		<section id="oss" className="border-y border-line bg-bg2 px-4 py-16 md:px-8">
			<div className="mx-auto max-w-[1200px]">
				<SectionHeading eyebrow="Open Source" title="Built in the Open. Always.">
					Loco is fully open source and always will be. Read the code, contribute, or self-host—it&apos;s yours to
					use.
				</SectionHeading>
				<div className="grid gap-4 md:grid-cols-3">
					<Tile icon={<GitHubIcon />} title="Full Source on GitHub">
						<p className="m-0 mb-2">
							Every line of Loco&apos;s code is public. Browse the source, fork it, run it yourself. No hidden
							pieces, no proprietary black boxes.
						</p>
						<TextLink href={GITHUB_URL}>team-loco/loco →</TextLink>
					</Tile>
					<Tile icon={<FileCode2 />} title="Built on Open Standards">
						<p className="m-0 mb-3">
							Kubernetes, OpenTelemetry, Envoy, Cilium—we build on OSS foundations you already know and trust.
						</p>
						<div className="flex flex-wrap gap-1.5">
							{OPEN_STANDARDS.map((tag) => (
								<Badge key={tag} tone="outline">
									{tag}
								</Badge>
							))}
						</div>
					</Tile>
					<Tile icon={<MessageSquare />} title="We Want Your Feedback">
						<p className="m-0 mb-2">
							Found a bug? Have a feature idea? Open a GitHub issue. We read every one and take them seriously.
						</p>
						<TextLink href={GITHUB_ISSUES_URL}>Open an issue →</TextLink>
					</Tile>
				</div>
			</div>
		</section>
	);
}

export function CtaSection({ onStart }: { onStart: () => void }) {
	return (
		<section className="px-4 py-16 md:px-8">
			<div className="mx-auto flex max-w-[1200px] flex-col items-center gap-4 rounded-lg border border-line bg-background px-6 py-14 text-center">
				<h2 className="m-0 text-[32px] leading-tight font-semibold tracking-[-0.02em]">Ready to Deploy?</h2>
				<p className="m-0 max-w-xl text-md leading-relaxed text-fg2">
					Join developers shipping with confidence. Get started in minutes—no credit card required.
				</p>
				<div className="mt-2 flex flex-wrap justify-center gap-3">
					<Button size="xl" onClick={onStart}>
						Start Deploying Free
					</Button>
					<Button
						variant="outline"
						size="xl"
						nativeButton={false}
						render={<a href={GITHUB_URL} target="_blank" rel="noreferrer" />}
					>
						<GitHubIcon className="size-4" />
						Star on GitHub
					</Button>
				</div>
			</div>
		</section>
	);
}
