import { LocoLogo } from "@/components/design/LocoLogo";

import { DOCS_URL, GITHUB_URL } from "./links";

export function SplashFooter({ onSignIn }: { onSignIn: () => void }) {
	return (
		<footer className="border-t border-background/10 bg-foreground text-background/55 [--logo:var(--logo-inverse)]">
			<div className="mx-auto flex max-w-[1200px] flex-wrap items-center gap-x-[22px] gap-y-3 px-4 py-[22px] text-[13px] sm:px-7">
				<LocoLogo className="w-12" />
				<span>Built in the open by team-loco.</span>
				<div className="flex-1" />
				<div className="flex items-center gap-[22px]">
					<a href={GITHUB_URL} target="_blank" rel="noreferrer" className="transition-colors hover:text-background">
						GitHub
					</a>
					<a href={DOCS_URL} target="_blank" rel="noreferrer" className="transition-colors hover:text-background">
						Docs
					</a>
					<button type="button" onClick={onSignIn} className="transition-colors hover:text-background">
						Sign in
					</button>
				</div>
			</div>
		</footer>
	);
}
