import { BoxIcon, ChartLineIcon, ScrollTextIcon } from "lucide-react";
import { useNavigate } from "react-router";

import { SoonTag } from "@/components/design/SoonTag";
import { useCopy } from "@/hooks/useCopy";
import { resourcePath } from "@/lib/routes";
import { formatClock, formatHourMinute } from "@/lib/time";
import { cn } from "@/lib/utils";

import { fitRange, useObs } from "./context";
import { severityStyle, type EventGroup } from "./events";
import { fmtTs } from "./format";
import { CopyButton, DetailPanel, JumpButton, PanelNav } from "./shared";

const OCC_BUCKETS = 24;

export function EventDetail({
	item,
	nowMs,
	onPrev,
	onNext,
	onClose,
}: {
	item: EventGroup;
	nowMs: number;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
}) {
	const { orgId, workspaceId, resourceByName, goTo } = useObs();
	const navigate = useNavigate();
	const [copied, copy] = useCopy();
	const st = severityStyle(item.severity);
	const res = resourceByName.get(item.resourceName);
	const first = Math.min(...item.occ);
	const last = Math.max(...item.occ);
	const span = Math.max(1, last - first);
	const ob = Array.from({ length: OCC_BUCKETS }, () => 0);
	for (const t of item.occ) {
		const i = Math.min(OCC_BUCKETS - 1, Math.floor(((t - first) / span) * OCC_BUCKETS));
		ob[i] = (ob[i] ?? 0) + 1;
	}
	const obmax = Math.max(1, ...ob);
	const range = fitRange(item.ts, nowMs);

	const fields: { k: string; v: string; copyable?: boolean; soon?: boolean }[] = [
		{ k: "Resource", v: item.resourceName },
		{ k: "Object", v: item.object, copyable: true },
		{ k: "Deployment", v: "", soon: true },
		{ k: "Region", v: res?.region === undefined || res.region === "" ? "—" : res.region },
		{ k: "Last seen", v: fmtTs(last, true) },
		{ k: "First seen", v: fmtTs(first, true) },
	];

	return (
		<DetailPanel
			header={
				<>
					<span className={cn("size-2 rounded-[2px]", st.dot)} />
					<span className={cn("min-w-0 truncate font-semibold", st.fg)}>{item.reason}</span>
					<span className={cn("inline-flex h-5 items-center rounded-sm px-[7px] text-xs font-medium", st.badge)}>{st.label}</span>
					<div className="flex-1" />
					<PanelNav onPrev={onPrev} onNext={onNext} onClose={onClose} />
				</>
			}
		>
			<div className="flex flex-1 flex-col overflow-y-auto">
				<div className="border-b border-line p-3.5 font-mono text-[12.5px] leading-[1.55] break-words whitespace-pre-wrap">
					{item.message}
				</div>
				<div className="grid grid-cols-[104px_minmax(0,1fr)] gap-x-3 gap-y-2 border-b border-line p-3.5">
					{fields.map((f) => (
						<div key={f.k} className="contents">
							<span className="text-fg3">{f.k}</span>
							<span className="flex min-w-0 items-center gap-1.5 font-mono text-sm break-all">
								{f.soon === true ? <SoonTag /> : f.v}
								{f.copyable === true && (
									<CopyButton
										copied={copied === f.k}
										onCopy={() => {
											copy(f.k, f.v);
										}}
										className="size-5"
									/>
								)}
							</span>
						</div>
					))}
				</div>
				<div className="flex flex-col gap-1.5 border-b border-line p-3.5">
					<JumpButton
						icon={<ScrollTextIcon />}
						label={`Logs from ${item.resourceName}`}
						hint={`around ${formatHourMinute(item.ts)}`}
						onClick={() => {
							goTo("logs", { range, resource: item.resourceName, focusTs: item.ts });
						}}
					/>
					<JumpButton
						icon={<ChartLineIcon />}
						label={`Metrics at ${formatClock(item.ts)}`}
						hint={item.resourceName}
						onClick={() => {
							goTo("metrics", { range, resource: item.resourceName, focusTs: item.ts });
						}}
					/>
					<JumpButton
						icon={<BoxIcon />}
						label={`Open ${item.resourceName}`}
						onClick={() => {
							void navigate(resourcePath(orgId, workspaceId, item.resourceId));
						}}
					/>
				</div>
				{item.occ.length > 1 && (
					<div className="flex flex-col gap-2 p-3.5">
						<span className="font-semibold">
							Occurrences <span className="font-normal text-fg3">{item.occ.length} times</span>
						</span>
						<div className="flex h-9 items-end gap-0.5">
							{ob.map((n, i) => (
								<div
									key={String(i)}
									title={`${String(n)} at ${fmtTs(first + (i * span) / OCC_BUCKETS)}`}
									className={cn("min-h-px flex-1 rounded-[1px]", n > 0 ? st.dot : "bg-bg3")}
									style={{ height: `${String((n / obmax) * 100)}%` }}
								/>
							))}
						</div>
						<div className="flex justify-between text-xs text-fg4">
							<span>{fmtTs(first)}</span>
							<span>{fmtTs(last)}</span>
						</div>
					</div>
				)}
			</div>
		</DetailPanel>
	);
}
