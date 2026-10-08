import { ArrowUpRight } from "lucide-react";
import type { ReactNode } from "react";

import { LocoLogo } from "@/components/design/LocoLogo";

import { DOCS_URL, EXAMPLES_URL, GITHUB_URL } from "./links";

const LINK_CLASS = "flex w-fit items-center gap-1 transition-colors hover:text-background";

function Column({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-[9px]">
			<span className="font-mono text-[11.5px] tracking-[0.08em] text-background/75 uppercase">{title}</span>
			{children}
		</div>
	);
}

function ExternalLink({ href, children }: { href: string; children: string }) {
	return (
		<a href={href} target="_blank" rel="noopener" className={LINK_CLASS}>
			{children}
			<ArrowUpRight className="size-3 opacity-70" />
		</a>
	);
}

export function SplashFooter({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	return (
		<footer className="border-t border-background/10 bg-foreground text-background/55 [--logo:var(--logo-inverse)]">
			<div className="mx-auto grid max-w-[1200px] grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-8 px-4 py-12 text-[13.5px] sm:px-7">
				<div className="flex flex-col gap-2.5">
					<LocoLogo className="w-[72px] self-start" />
					<span>Built by developers, for developers.</span>
				</div>
				<Column title="Product">
					<a href="#product" className={LINK_CLASS}>
						Live demo
					</a>
					<a href="#start" className={LINK_CLASS}>
						Get started
					</a>
					<a href="#stack" className={LINK_CLASS}>
						Architecture
					</a>
				</Column>
				<Column title="Developers">
					<ExternalLink href={DOCS_URL}>Docs</ExternalLink>
					<ExternalLink href={GITHUB_URL}>GitHub</ExternalLink>
					<ExternalLink href={EXAMPLES_URL}>Examples</ExternalLink>
				</Column>
				<Column title="Account">
					<button type="button" onClick={onSignIn} disabled={redirecting} className={LINK_CLASS}>
						Sign in
					</button>
					<a href="#access" className={LINK_CLASS}>
						Request access
					</a>
				</Column>
			</div>
		</footer>
	);
}
