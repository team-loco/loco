import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function SectionTitle({ children, className }: { children: string; className?: string | undefined }) {
	return (
		<h2
			className={cn(
				"m-0 mb-10 text-[clamp(30px,3.6vw,44px)] leading-[1.08] font-medium tracking-[-0.035em] text-balance",
				className,
			)}
		>
			{children}
		</h2>
	);
}

export function Section({
	id,
	className,
	children,
}: {
	id?: string | undefined;
	className?: string | undefined;
	children: ReactNode;
}) {
	return (
		<section id={id} className="scroll-mt-16 pt-20 sm:pt-28">
			<div className={cn("mx-auto w-full max-w-[1160px] px-4 sm:px-7", className)}>{children}</div>
		</section>
	);
}
