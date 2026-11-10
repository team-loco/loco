import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { Fragment, type ReactNode } from "react";

import { Section } from "@/components/design/Page";
import { formatMonthDay, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

export function formatDay(ts: Timestamp | undefined): string {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return "—";
	const sameYear = new Date(ms).getFullYear() === new Date().getFullYear();
	return sameYear ? formatMonthDay(ms) : formatMonthDayYear(ms);
}

export function SettingsCard({ className, children }: { className?: string; children: ReactNode }) {
	return <section className={cn("min-w-0 overflow-hidden rounded-card bg-panel", className)}>{children}</section>;
}

export function SettingsSection(props: {
	title: string;
	count?: number | undefined;
	meta?: ReactNode;
	actions?: ReactNode;
	children: ReactNode;
}) {
	return <Section {...props} card={false} className="max-w-[960px]" />;
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
