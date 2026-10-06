import { ArrowUpRight } from "lucide-react";

import { Button } from "@/components/design/Button";
import { LocoLogo } from "@/components/design/LocoLogo";

import { GitHubIcon } from "./GitHubIcon";
import { DOCS_URL, GITHUB_URL } from "./links";

const NAV_LINKS = [
	{ label: "Product", href: "#product" },
	{ label: "Get started", href: "#start" },
	{ label: "Architecture", href: "#stack" },
] as const;

export function SplashNav({ onSignIn }: { onSignIn: () => void }) {
	return (
		<header className="sticky top-0 z-30 border-b border-line bg-background/80 backdrop-blur-md backdrop-saturate-150">
			<div className="mx-auto flex h-[60px] max-w-[1200px] items-center gap-4 px-4 sm:gap-8 sm:px-7">
				<a href="/" className="flex items-center" aria-label="Loco home">
					<LocoLogo className="w-[72px]" />
				</a>
				<nav className="hidden gap-[26px] text-md md:flex">
					{NAV_LINKS.map((link) => (
						<a key={link.label} href={link.href} className="text-fg2 transition-colors hover:text-primary">
							{link.label}
						</a>
					))}
					<a
						href={DOCS_URL}
						target="_blank"
						rel="noopener"
						title="Opens buf.build in a new tab"
						className="flex items-center gap-[3px] text-fg2 transition-colors hover:text-primary"
					>
						Docs
						<ArrowUpRight className="size-[13px] text-fg4" />
					</a>
				</nav>
				<div className="flex-1" />
				<div className="flex items-center gap-1 sm:gap-5">
					<a
						href={GITHUB_URL}
						target="_blank"
						rel="noopener"
						className="hidden items-center gap-1.5 text-md text-fg2 transition-colors hover:text-primary sm:flex"
					>
						<GitHubIcon className="size-4" />
						GitHub
						<ArrowUpRight className="-ml-0.5 size-[13px] text-fg4" />
					</a>
					<Button variant="ghost" onClick={onSignIn} className="px-2 text-md text-fg2 hover:text-foreground">
						Sign in
					</Button>
					<Button
						variant="outline"
						size="lg"
						nativeButton={false}
						render={<a href="#access" />}
						className="rounded-xl text-md font-medium hover:border-foreground"
					>
						Request access
					</Button>
				</div>
			</div>
		</header>
	);
}
