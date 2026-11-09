import { ArrowUpRight } from "lucide-react";

import { Button } from "@/components/design/Button";
import { LocoLogo } from "@/components/design/LocoLogo";

import { GitHubIcon } from "./GitHubIcon";
import { DOCS_URL, GITHUB_URL } from "./links";

const NAV_LINKS = [
	{ label: "How it works", href: "#start" },
	{ label: "Features", href: "#features" },
	{ label: "Architecture", href: "#stack" },
] as const;

const LINK_CLASS = "inline-flex items-center text-fg3 transition-colors hover:text-foreground";

export function SplashNav({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	return (
		<header className="sticky top-0 z-30 border-b border-line bg-background/80 backdrop-blur-md backdrop-saturate-150">
			<div className="mx-auto flex h-[58px] max-w-[1160px] items-center gap-7 px-4 sm:px-7">
				<a href="/" className="flex items-center" aria-label="Loco home">
					<LocoLogo className="w-[72px]" />
				</a>
				<nav aria-label="Main" className="hidden gap-[22px] text-[13.5px] min-[760px]:flex">
					{NAV_LINKS.map((link) => (
						<a key={link.label} href={link.href} className={LINK_CLASS}>
							{link.label}
						</a>
					))}
					<a href={DOCS_URL} target="_blank" rel="noopener" className={LINK_CLASS}>
						Docs
						<ArrowUpRight aria-hidden="true" className="ml-0.5 size-3 text-fg4" />
					</a>
				</nav>
				<div className="flex-1" />
				<a href={GITHUB_URL} target="_blank" rel="noopener" aria-label="Loco source code" className={LINK_CLASS}>
					<GitHubIcon className="size-[18px]" />
				</a>
				<Button variant="outline" onClick={onSignIn} disabled={redirecting} className="rounded-[7px] font-medium">
					Sign in
				</Button>
			</div>
		</header>
	);
}
