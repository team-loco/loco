import { useId, type ReactNode } from "react";

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

const CARD = "min-w-0 rounded-card bg-panel";

export function Section({
	title,
	count,
	meta,
	actions,
	filters,
	search,
	card = true,
	cardTitle,
	cardActions,
	className,
	bodyClassName,
	children,
}: {
	title?: ReactNode;
	count?: number | undefined;
	meta?: ReactNode;
	actions?: ReactNode;
	filters?: ReactNode;
	search?: ReactNode;
	card?: boolean;
	cardTitle?: ReactNode;
	cardActions?: ReactNode;
	className?: string;
	bodyClassName?: string;
	children?: ReactNode;
}) {
	const headingId = useId();
	const body = (
		<>
			{(cardTitle !== undefined || cardActions !== undefined) && (
				<div className="flex flex-wrap items-center gap-3.5 px-5 pt-4 pb-3.5">
					{cardTitle !== undefined && (
						<h2 className="m-0 mr-auto flex items-baseline gap-3 text-[22px] leading-[1.15] font-normal tracking-[-0.03em]">
							{cardTitle}
						</h2>
					)}
					{cardActions}
				</div>
			)}
			{(filters !== undefined || search !== undefined) && (
				<div className="flex flex-wrap items-center gap-2.5 px-5 pt-3.5 pb-2.5">
					{filters}
					{search !== undefined && <div className="ml-auto min-w-40 flex-[0_1_280px] max-sm:grow">{search}</div>}
				</div>
			)}
			<div className={bodyClassName}>{children}</div>
		</>
	);
	if (title === undefined) return <section className={cn(CARD, className)}>{body}</section>;
	return (
		<section aria-labelledby={headingId} className={cn("flex min-w-0 flex-col gap-2.5", className)}>
			<div className="flex flex-wrap items-baseline gap-3 pt-1.5">
				<h2 id={headingId} className="m-0 flex items-baseline gap-2 text-[18px] font-normal tracking-[-0.02em]">
					{title}
				</h2>
				{count !== undefined && <span className="font-mono text-[12.5px] text-fg3">{count}</span>}
				{meta !== undefined && <span className="text-[12.5px] text-fg3">{meta}</span>}
				{actions !== undefined && <div className="ml-auto flex items-center gap-2 self-center">{actions}</div>}
			</div>
			{card ? <div className={CARD}>{body}</div> : children}
		</section>
	);
}
