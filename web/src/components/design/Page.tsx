import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Page({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<div
			className={cn(
				"mx-auto flex w-full max-w-[1240px] min-w-0 flex-col gap-7 px-[clamp(16px,4vw,48px)] pt-7 pb-24",
				className,
			)}
		>
			{children}
		</div>
	);
}

export function PageHeader({
	title,
	children,
	actions,
}: {
	title: ReactNode;
	children?: ReactNode;
	actions?: ReactNode;
}) {
	return (
		<div className="flex flex-wrap items-end gap-4">
			<h1 className="m-0 flex items-center gap-3 text-[28px] leading-[1.15] font-normal tracking-[-0.03em]">{title}</h1>
			{children}
			<div className="flex-1" />
			{actions}
		</div>
	);
}

export function Section({
	title,
	actions,
	toolbar,
	className,
	bodyClassName,
	children,
}: {
	title?: ReactNode;
	actions?: ReactNode;
	toolbar?: ReactNode;
	className?: string;
	bodyClassName?: string;
	children?: ReactNode;
}) {
	return (
		<section className={cn("min-w-0 rounded-[10px] bg-panel", className)}>
			{(title !== undefined || actions !== undefined || toolbar !== undefined) && (
				<div className="flex flex-wrap items-center gap-3.5 px-5 pt-4 pb-3.5">
					{title !== undefined && (
						<h2 className="m-0 mr-auto flex items-baseline gap-3 text-[22px] leading-[1.15] font-normal tracking-[-0.03em]">
							{title}
						</h2>
					)}
					{toolbar}
					{actions}
				</div>
			)}
			<div className={bodyClassName}>{children}</div>
		</section>
	);
}

export function SectionFooter({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<div
			className={cn(
				"flex items-center justify-between border-t border-line2 px-5 pt-3 pb-3.5 text-[12.5px] text-fg3",
				className,
			)}
		>
			{children}
		</div>
	);
}
