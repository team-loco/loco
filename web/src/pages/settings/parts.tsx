import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { Fragment, type ReactNode } from "react";

import { Section } from "@/components/design/Page";
import { formatMonthDay, formatMonthDayYear, formatIsoDate, maybeTsMs, type TimePreferences } from "@/lib/time";
import { cn } from "@/lib/utils";

export function formatDay(ts: Timestamp | undefined, pref: TimePreferences): string {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return "—";
	const sameYear = formatIsoDate(ms, pref).slice(0, 4) === formatIsoDate(Date.now(), pref).slice(0, 4);
	return sameYear ? formatMonthDay(ms, pref) : formatMonthDayYear(ms, pref);
}

export function SettingsCard({ className, children }: { className?: string; children: ReactNode }) {
	return <section className={cn("min-w-0 overflow-hidden rounded-card bg-panel", className)}>{children}</section>;
}

export function SettingsSection(props: {
	id?: string;
	title: string;
	count?: number | undefined;
	meta?: ReactNode;
	actions?: ReactNode;
	children: ReactNode;
}) {
	return <Section {...props} card={false} className="max-w-[960px] scroll-mt-20" />;
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
