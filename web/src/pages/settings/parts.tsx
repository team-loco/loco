import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { CheckIcon, CopyIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { formatMonthDay, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

export function formatDay(ts: Timestamp | undefined): string {
	const ms = maybeTsMs(ts);
	if (ms === undefined) return "—";
	const sameYear = new Date(ms).getFullYear() === new Date().getFullYear();
	return sameYear ? formatMonthDay(ms) : formatMonthDayYear(ms);
}

export function SettingsCard({ children }: { children: ReactNode }) {
	return <section className="overflow-hidden rounded-lg border border-line bg-background">{children}</section>;
}

export function SettingsRow({
	label,
	hint,
	controlId,
	hintId,
	last,
	children,
}: {
	label: string;
	hint?: string | undefined;
	controlId?: string | undefined;
	hintId?: string | undefined;
	last?: boolean | undefined;
	children: ReactNode;
}) {
	return (
		<div
			className={cn(
				"grid grid-cols-1 gap-3 px-5 py-[18px] md:grid-cols-[240px_minmax(0,1fr)] md:gap-6",
				last !== true && "border-b border-line",
			)}
		>
			<div className="flex flex-col gap-1">
				{controlId === undefined ? (
					<span className="font-semibold">{label}</span>
				) : (
					<label htmlFor={controlId} className="font-semibold">
						{label}
					</label>
				)}
				{hint !== undefined && (
					<span id={hintId} className="text-[12.5px] leading-[1.45] text-fg3">
						{hint}
					</span>
				)}
			</div>
			<div className="flex max-w-[520px] min-w-0 flex-col gap-1.5">{children}</div>
		</div>
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
