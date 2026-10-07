import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { CheckIcon, CopyIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { formatMonthDay, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

export const NAME_RE = /^[a-z0-9][a-z0-9-]*$/;

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
	last,
	children,
}: {
	label: string;
	hint?: string | undefined;
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
				<span className="font-semibold">{label}</span>
				{hint !== undefined && <span className="text-[12.5px] leading-[1.45] text-fg3">{hint}</span>}
			</div>
			<div className="flex max-w-[520px] min-w-0 flex-col gap-1.5">{children}</div>
		</div>
	);
}

export function SaveBar({
	onDiscard,
	onSave,
	disabled,
	pending,
}: {
	onDiscard: () => void;
	onSave: () => void;
	disabled: boolean;
	pending: boolean;
}) {
	return (
		<div className="flex items-center justify-end gap-2 bg-bg2 px-5 py-3">
			<span className="flex-1 text-[12.5px] text-fg3">Unsaved changes</span>
			<Button variant="outline" size="lg" className="px-3" onClick={onDiscard} disabled={pending}>
				Discard
			</Button>
			<Button size="lg" onClick={onSave} disabled={disabled || pending}>
				{pending ? "Saving…" : "Save"}
			</Button>
		</div>
	);
}

export function SavedBar() {
	return (
		<div className="flex items-center gap-1.5 bg-ok-bg px-5 py-3 text-[12.5px] font-medium text-ok-fg">
			<CheckIcon className="size-3.5" />
			Saved
		</div>
	);
}

export function DangerCard({
	title,
	subtitle,
	subtitleWarn,
	label,
	blockedReason,
	onClick,
}: {
	title: string;
	subtitle: ReactNode;
	subtitleWarn?: boolean | undefined;
	label: string;
	blockedReason: string | null;
	onClick: () => void;
}) {
	const button = (
		<Button
			variant="destructive-outline"
			size="lg"
			className="px-3.5"
			disabled={blockedReason !== null}
			onClick={onClick}
		>
			{label}
		</Button>
	);
	return (
		<section className="overflow-hidden rounded-lg border border-[color-mix(in_oklab,var(--red)_35%,var(--line))] bg-background">
			<div className="flex flex-wrap items-center gap-5 px-5 py-[18px]">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="font-semibold">{title}</span>
					<span className={cn("text-[12.5px]", subtitleWarn === true ? "text-warn-fg" : "text-fg3")}>
						{subtitle}
					</span>
				</div>
				{blockedReason === null ? (
					button
				) : (
					<Tooltip>
						<TooltipTrigger render={<span className="inline-flex cursor-not-allowed" />}>{button}</TooltipTrigger>
						<TooltipContent>{blockedReason}</TooltipContent>
					</Tooltip>
				)}
			</div>
		</section>
	);
}

export function CopyField({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
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
