import { Button } from "@/components/design/Button";
import { QueryErrorLine } from "@/pages/observability/QueryBar";
import { AUDIT_FIELDS, type auditSearchError } from "./queryError";

export function AuditQueryError({
	rejected,
	onField,
}: {
	rejected: NonNullable<ReturnType<typeof auditSearchError>>;
	onField: (field: string) => void;
}) {
	return (
		<div
			id="audit-query-error"
			className="m-4 flex flex-col gap-4 rounded-control border border-bad-fg/20 bg-bad-bg px-4 py-3 md:flex-row md:justify-between"
		>
			<QueryErrorLine error={rejected.error} highlightTo={rejected.end} />
			<div className="flex shrink-0 flex-col gap-1.5 text-[12px]">
				<span className="text-[10.5px] font-medium tracking-wide text-fg3 uppercase">Fields</span>
				{AUDIT_FIELDS.map((field) => (
					<Button
						key={field.key}
						variant="ghost"
						className="h-auto justify-start gap-3 px-1 py-1 text-left font-normal"
						onClick={() => {
							onField(field.key);
						}}
					>
						<span className="w-[100px] shrink-0 font-mono text-[12px]">@{field.key}:</span>
						<span className="text-[12px] text-fg3">{field.label}</span>
					</Button>
				))}
			</div>
		</div>
	);
}
