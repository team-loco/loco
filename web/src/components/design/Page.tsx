import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Page({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<div className={cn("flex w-full max-w-[1440px] min-w-0 flex-col gap-6 px-4 pt-3 pb-16 md:px-8", className)}>
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
		<div className="flex flex-wrap items-center gap-3">
			<h1 className="m-0 text-2xl font-semibold tracking-[-0.01em]">{title}</h1>
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
		<section className={cn("min-w-0 rounded-lg border border-line bg-background", className)}>
			{(title !== undefined || actions !== undefined || toolbar !== undefined) && (
				<div className="flex flex-wrap items-center gap-2.5 border-b border-line px-4 py-3">
					{title !== undefined && <span className="mr-1.5 text-lg font-semibold">{title}</span>}
					{toolbar}
					{actions !== undefined && (
						<>
							<div className="flex-1" />
							{actions}
						</>
					)}
				</div>
			)}
			<div className={bodyClassName}>{children}</div>
		</section>
	);
}

export function SectionFooter({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<div className={cn("flex items-center justify-between px-4 py-2.5 text-sm text-fg3", className)}>
			{children}
		</div>
	);
}
