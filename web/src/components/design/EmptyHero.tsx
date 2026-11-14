import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function EmptyHero({
	art,
	title,
	subtitle,
	children,
	className,
	role,
}: {
	art: ReactNode;
	title: ReactNode;
	subtitle?: ReactNode;
	children?: ReactNode;
	className?: string | undefined;
	role?: "alert";
}) {
	return (
		<section
			role={role}
			className={cn(
				"flex flex-col items-center gap-3 rounded-overlay bg-panel px-6 pt-14 pb-16 text-center",
				className,
			)}
		>
			{art}
			<h2 className="m-0 mt-2 text-2xl font-normal tracking-[-0.03em]">{title}</h2>
			{subtitle !== undefined && <p className="m-0 max-w-[34em] text-md leading-[1.55] text-fg3">{subtitle}</p>}
			{children}
		</section>
	);
}
