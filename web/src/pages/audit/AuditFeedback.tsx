import { RotateCcwIcon } from "lucide-react";

import { Banner } from "@/components/design/Banner";
import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorReceipt } from "@/components/design/ErrorState";
import { AUDIT_FILTER_FIXES, auditFilterViolations, type DroppedFilter } from "./link";
import { DEFAULT_RANGE } from "./model";

export function DroppedFilters({ filters, onDismiss }: { filters: DroppedFilter[]; onDismiss: () => void }) {
	return (
		<Banner
			tone="warn"
			title="Some filters in this link couldn’t be used and were left out"
			className="mx-5 mt-0 mb-3.5"
			action={
				<Button variant="ghost" size="sm" className="shrink-0" onClick={onDismiss}>
					Dismiss
				</Button>
			}
		>
			<ul className="mt-1 flex list-none flex-col gap-1 p-0 text-[12.5px]">
				{filters.map((f, i) => (
					<li key={i} className="flex flex-wrap gap-x-2.5">
						<span className="text-fg3">{f.label}</span>
						<s className="break-all font-mono text-[12px] text-fg2 decoration-fg3">{f.value}</s>
						<span className="text-fg2">{f.reason}</span>
					</li>
				))}
			</ul>
		</Banner>
	);
}

export function AuditFilterError({ error, onFix }: { error: Error; onFix: (field: string) => void }) {
	const violations = auditFilterViolations(error);
	const field = violations.find((v) => AUDIT_FILTER_FIXES.has(v.field))?.field ?? "";
	const fix = AUDIT_FILTER_FIXES.get(field);
	const time = field === "start_time" || field === "end_time";
	const subject = new Map([
		["page_size", "number of rows"],
		["before_seq", "page"],
		["workspace_id", "workspace"],
		["resource_id", "resource"],
		["environment_id", "environment"],
	]).get(field);
	return (
		<EmptyHero
			role="alert"
			className="rounded-none border-t border-line bg-transparent pt-12 pb-14"
			art={
				<ErrorReceipt
					error={error}
					rpc="EventService.ListEvents"
					className="w-[min(520px,100%)]"
					rows={violations.map((v) => ({ label: "field", value: `${v.field}: ${v.description}` }))}
				/>
			}
			title={
				time
					? "Couldn’t load events for this time range"
					: subject === undefined
						? "Couldn’t load events with these filters"
						: `Couldn’t load events for this ${subject}`
			}
			subtitle={time ? "Choose a different time range to load events." : "Check the filters and try again."}
		>
			{fix !== undefined && (
				<Button
					onClick={() => {
						onFix(field);
					}}
				>
					<RotateCcwIcon />
					{time ? `Reset to ${DEFAULT_RANGE.label}` : fix.label}
				</Button>
			)}
		</EmptyHero>
	);
}
