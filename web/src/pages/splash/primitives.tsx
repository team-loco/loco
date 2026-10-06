import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

function CornerMark({ className }: { className: string }) {
	return (
		<span aria-hidden="true" className={cn("pointer-events-none absolute -top-1.5 size-[11px]", className)}>
			<span className="absolute top-0 left-[5px] h-[11px] w-px bg-fg4" />
			<span className="absolute top-[5px] left-0 h-px w-[11px] bg-fg4" />
		</span>
	);
}

export function CornerMarks() {
	return (
		<>
			<CornerMark className="-left-1.5" />
			<CornerMark className="-right-1.5" />
		</>
	);
}

export function Frame({ className, children }: { className?: string | undefined; children: ReactNode }) {
	return (
		<div className={cn("relative mx-auto w-full max-w-[1200px] border-x border-line px-4 sm:px-7", className)}>
			{children}
		</div>
	);
}

export function SectionHeading({
	index,
	eyebrow,
	title,
	className,
	children,
}: {
	index: string;
	eyebrow: string;
	title: string;
	className?: string | undefined;
	children?: ReactNode;
}) {
	return (
		<div className={cn("flex max-w-[640px] flex-col gap-3.5", className)}>
			<span className="font-mono text-sm tracking-[0.08em] text-fg3 uppercase">
				<span className="text-primary">{index}</span> / {eyebrow}
			</span>
			<h2 className="m-0 text-[clamp(32px,4vw,48px)] leading-[1.08] font-semibold tracking-[-0.03em] text-balance">
				{title}
			</h2>
			{children}
		</div>
	);
}
