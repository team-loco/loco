import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function EmptyHero({
	art,
	title,
	subtitle,
	children,
	className,
}: {
	art: ReactNode;
	title: ReactNode;
	subtitle?: ReactNode;
	children?: ReactNode;
	className?: string;
}) {
	return (
		<section
			className={cn(
				"flex flex-col items-center gap-4 rounded-lg border border-line bg-background bg-[radial-gradient(var(--pe-dot)_1px,transparent_1px)] bg-size-[16px_16px] px-6 pt-14 pb-16 text-center",
				className,
			)}
		>
			{art}
			<div className="mt-2 flex flex-col items-center gap-1.5">
				<span className="text-[18px] font-semibold tracking-[-0.01em]">{title}</span>
				{subtitle !== undefined && <span className="text-fg2">{subtitle}</span>}
			</div>
			{children}
		</section>
	);
}
