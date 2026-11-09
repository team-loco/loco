import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { CheckIcon, CopyIcon } from "lucide-react";
import { Fragment, useId, type ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { formatMonthDay, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

export function formatDay(ts: Timestamp | undefined): string {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return "—";
	const sameYear = new Date(ms).getFullYear() === new Date().getFullYear();
	return sameYear ? formatMonthDay(ms) : formatMonthDayYear(ms);
}

export function SettingsCard({ className, children }: { className?: string; children: ReactNode }) {
	return <div className={cn("min-w-0 overflow-hidden rounded-[10px] bg-panel", className)}>{children}</div>;
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

export function CopyField({
	label,
	value,
	copied,
	onCopy,
}: {
	label: string;
	value: string;
	copied: boolean;
	onCopy: () => void;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<span className="text-[12px] text-fg3">{label}</span>
			<div className="flex min-w-0 items-center gap-1.5 rounded-sm border border-line bg-bg2 py-1 pr-1 pl-2.5">
				<code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{value}</code>
				<Button variant="ghost" size="icon-sm" aria-label={`Copy ${label}`} onClick={onCopy}>
					{copied ? <CheckIcon /> : <CopyIcon />}
				</Button>
			</div>
		</div>
	);
}
