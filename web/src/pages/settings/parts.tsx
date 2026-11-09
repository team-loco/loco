import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { Fragment, useId, type ReactNode } from "react";

import { formatMonthDay, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

export function formatDay(ts: Timestamp | undefined): string {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return "—";
	const sameYear = new Date(ms).getFullYear() === new Date().getFullYear();
	return sameYear ? formatMonthDay(ms) : formatMonthDayYear(ms);
}

export function SettingsCard({
	className,
	labelledBy,
	children,
}: {
	className?: string;
	labelledBy?: string;
	children: ReactNode;
}) {
	return (
		<section aria-labelledby={labelledBy} className={cn("min-w-0 overflow-hidden rounded-card bg-panel", className)}>
			{children}
		</section>
	);
}

export function SettingsSection({
	title,
	meta,
	actions,
	children,
}: {
	title: string;
	meta?: ReactNode;
	actions?: ReactNode;
	children: ReactNode;
}) {
	const id = useId();
	return (
		<section aria-labelledby={id} className="flex max-w-[960px] min-w-0 flex-col gap-2.5">
			<div className="flex flex-wrap items-baseline gap-3 pt-1.5">
				<h2 id={id} className="m-0 text-[18px] font-normal tracking-[-0.02em]">
					{title}
				</h2>
				{meta !== undefined && <span className="font-mono text-[12.5px] text-fg3">{meta}</span>}
				{actions !== undefined && <span className="ml-auto self-center">{actions}</span>}
			</div>
			{children}
		</section>
	);
}

export function KeyValues({ items }: { items: readonly (readonly [string, ReactNode])[] }) {
	return (
		<dl className="m-0 grid grid-cols-[minmax(110px,max-content)_minmax(0,1fr)] items-center gap-x-5 gap-y-2.5 text-[13px]">
			{items.map(([key, value]) => (
				<Fragment key={key}>
					<dt className="text-fg3">{key}</dt>
					<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{value}</dd>
				</Fragment>
			))}
		</dl>
	);
}
