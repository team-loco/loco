import { Button } from "@/components/design/Button";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/design/InputGroup";
import { SoonTag } from "@/components/design/SoonTag";

import { GitHubIcon } from "./GitHubIcon";

export function AccessSection() {
	return (
		<section id="access" className="relative scroll-mt-16 overflow-hidden bg-foreground text-background">
			<div className="relative mx-auto flex max-w-[680px] flex-col items-center gap-[18px] px-4 py-[120px] text-center sm:px-7">
				<h2 className="m-0 text-[clamp(36px,4.8vw,56px)] leading-[1.02] font-semibold tracking-[-0.035em]">
					Get an invite.
				</h2>
				<p className="m-0 max-w-[440px] text-[17px] leading-[1.55] text-balance text-background/65">
					Access is granted per GitHub account while Loco is in beta.
				</p>
				<div className="mt-2.5 flex w-full max-w-[440px] flex-wrap gap-2">
					<InputGroup className="h-[46px] min-w-[220px] flex-1 rounded-[9px] border-background/20 bg-background/5 has-disabled:bg-background/5 has-disabled:opacity-75 dark:bg-background/5 dark:has-disabled:bg-background/5">
						<InputGroupAddon className="pl-3.5 text-background/50">
							<GitHubIcon className="size-4" />
						</InputGroupAddon>
						<InputGroupInput
							disabled
							placeholder="github-username"
							aria-label="GitHub username"
							autoComplete="off"
							spellCheck={false}
							className="text-[15px] text-background placeholder:text-background/50 md:text-[15px]"
						/>
					</InputGroup>
					<Button
						variant="outline"
						disabled
						className="h-[46px] gap-2 rounded-[9px] max-sm:w-full border-transparent bg-background px-5 text-lg font-semibold text-foreground disabled:opacity-75 dark:border-transparent dark:bg-background"
					>
						Request access
						<SoonTag />
					</Button>
				</div>
			</div>
		</section>
	);
}
