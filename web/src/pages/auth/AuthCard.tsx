import type { ReactNode } from "react";
import { Link } from "react-router";

import { LocoLogo } from "@/components/design/LocoLogo";

import { LoginArt } from "./LoginArt";

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
		<main className="grid min-h-dvh grid-cols-1 bg-background text-foreground min-[900px]:grid-cols-[minmax(360px,1fr)_minmax(0,1.15fr)]">
			<div className="flex flex-col px-[clamp(24px,5vw,64px)] py-7">
				<div className="mx-auto flex w-full max-w-[360px] flex-1 flex-col justify-center gap-4 py-10">
					<div className="flex flex-col items-center gap-2 text-center">
						<Link to="/" aria-label="loco home" className="mb-2.5 rounded-lg">
							<LocoLogo className="w-[104px]" />
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
			</div>
			<LoginArt className="hidden min-[900px]:block" />
		</main>
	);
}
