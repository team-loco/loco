import type { ReactNode } from "react";
import { Link } from "react-router";

import { LocoLogo } from "@/components/design/LocoLogo";

export function AuthCard({
	title,
	subtitle,
	error,
	children,
}: {
	title: string;
	subtitle?: string | undefined;
	error?: string | null | undefined;
	children: ReactNode;
}) {
	return (
		<main className="flex min-h-dvh items-start justify-center bg-shell px-4 pt-[14vh] pb-10 text-foreground">
			<div className="flex w-full max-w-[400px] flex-col gap-5 rounded-[12px] border border-line bg-background px-8 pt-9 pb-8 shadow-popover max-[420px]:px-6 dark:border-line2">
				<div className="flex flex-col items-center gap-2 text-center">
					<Link to="/" aria-label="loco home" className="mb-3 rounded-lg">
						<LocoLogo className="w-24" />
					</Link>
					<h1 className="m-0 text-[30px] leading-[1.1] font-normal tracking-[-0.035em] text-balance">{title}</h1>
					{subtitle !== undefined && <p className="m-0 text-md text-fg3">{subtitle}</p>}
				</div>
				{error !== undefined && error !== null && (
					<div role="alert" className="rounded-lg bg-bad-bg px-3 py-2.5 text-[13px] text-bad-fg">
						{error}
					</div>
				)}
				{children}
			</div>
		</main>
	);
}
