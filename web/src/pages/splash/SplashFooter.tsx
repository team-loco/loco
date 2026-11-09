import { ArrowUpRight } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { LocoLogo } from "@/components/design/LocoLogo";
import { ThemeIcon } from "@/components/shell/ThemeIcon";
import { useTheme } from "@/lib/use-theme";

import { DOCS_URL, EXAMPLES_URL, GITHUB_URL } from "./links";

const LINK_CLASS = "inline-flex w-fit items-center text-fg2 transition-colors hover:text-foreground";

function Column({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-[9px] text-[13.5px]">
			<span className="mb-0.5 text-[12.5px] text-fg3">{title}</span>
			{children}
		</div>
	);
}

function ExternalLink({ href, children }: { href: string; children: string }) {
	return (
		<a href={href} target="_blank" rel="noopener" className={LINK_CLASS}>
			{children}
			<ArrowUpRight aria-hidden="true" className="ml-0.5 size-3 text-fg4" />
		</a>
	);
}

function ThemeToggle() {
	const { theme, toggle } = useTheme();
	const toDark = theme !== "dark";

	return (
		<Button
			variant="outline"
			size="icon"
			aria-label={toDark ? "Dark mode" : "Light mode"}
			title={toDark ? "Dark mode" : "Light mode"}
			onClick={toggle}
		>
			<ThemeIcon mode={toDark ? "moon" : "sun"} />
		</Button>
	);
}

export function SplashFooter({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	return (
		<footer className="mt-auto border-t border-line bg-panel">
			<div className="mx-auto grid w-full max-w-[1160px] grid-cols-2 gap-8 px-4 pt-11 pb-14 sm:px-7 min-[760px]:grid-cols-[1.4fr_repeat(3,1fr)]">
				<div className="flex flex-col items-start gap-[18px]">
					<LocoLogo className="w-[72px]" />
					<ThemeToggle />
				</div>
				<Column title="Product">
					<a href="#start" className={LINK_CLASS}>
						How it works
					</a>
					<a href="#features" className={LINK_CLASS}>
						Features
					</a>
					<a href="#stack" className={LINK_CLASS}>
						Architecture
					</a>
				</Column>
				<Column title="Developers">
					<ExternalLink href={DOCS_URL}>Docs</ExternalLink>
					<ExternalLink href={GITHUB_URL}>Source code</ExternalLink>
					<ExternalLink href={EXAMPLES_URL}>Examples</ExternalLink>
				</Column>
				<Column title="Account">
					<button type="button" onClick={onSignIn} disabled={redirecting} className={LINK_CLASS}>
						Sign in
					</button>
				</Column>
			</div>
		</footer>
	);
}
