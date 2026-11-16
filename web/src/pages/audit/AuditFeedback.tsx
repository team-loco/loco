import { TriangleAlertIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { requestId } from "@/lib/error-handler";
import { auditFilterViolations, type DroppedFilter } from "./link";

export function DroppedFilters({ filters, onDismiss }: { filters: DroppedFilter[]; onDismiss: () => void }) {
	return (
		<div
			role="status"
			className="m-4 rounded-control border border-warn/25 bg-warn-bg px-4 py-3 text-[13px] text-warn-fg"
		>
			<div className="flex items-start justify-between gap-3 font-medium">
				<span>Some filters in this link couldn't be used, so they were left out</span>
				<Button variant="ghost" size="icon-sm" aria-label="Dismiss unused filters" onClick={onDismiss}>
					<XIcon />
				</Button>
			</div>
			<ul className="mt-2 flex list-none flex-col gap-1 p-0">
				{filters.map((f, i) => (
					<li key={i} className="flex flex-wrap gap-x-2">
						<span>{f.label}</span>
						<s className="break-all font-mono">{f.value}</s>
						<span className="text-fg3">{f.reason}</span>
					</li>
				))}
			</ul>
		</div>
	);
}

export function AuditFilterError({ error, onFix }: { error: Error; onFix: (field: string) => void }) {
	const violations = auditFilterViolations(error);
	const id = requestId(error);
	return (
		<div
			role="alert"
			className="m-4 flex flex-col gap-2 rounded-control border border-bad-fg/20 bg-bad-bg px-4 py-3 text-[13px]"
		>
			<span className="flex items-center gap-2 font-medium text-bad-fg">
				<TriangleAlertIcon className="size-4" />
				The audit log rejected these filters
			</span>
			{violations.map((v, i) => (
				<div key={i} className="flex flex-wrap items-center gap-2">
					<span className="font-mono text-bad-fg">{v.field}</span>
					<span className="text-fg2">{v.description}</span>
					{fixLabel(v.field) !== null && (
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								onFix(v.field);
							}}
						>
							{fixLabel(v.field)}
						</Button>
					)}
				</div>
			))}
			{id !== null && (
				<CopyId value={id} title="Copy request ID" className="max-w-full self-start font-mono text-[12px]">
					<span className="truncate">{id}</span>
				</CopyId>
			)}
		</div>
	);
}

function fixLabel(field: string) {
	switch (field) {
		case "start_time":
		case "end_time":
			return "Reset time range";
		case "page_size":
			return "Reset rows per page";
		case "before_seq":
			return "Show newest events";
		case "workspace_id":
			return "Clear workspace filter";
		case "resource_id":
			return "Clear resource filter";
		case "environment_id":
			return "Clear environment filter";
		default:
			return null;
	}
}
