import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorReceipt } from "@/components/design/ErrorState";
import { QueryErrorText } from "@/pages/observability/QueryBar";
import { auditQueryFix, type auditSearchError } from "./queryError";

export function AuditQueryError({
	rejected,
	onField,
}: {
	rejected: NonNullable<ReturnType<typeof auditSearchError>>;
	onField: (field: string) => void;
}) {
	return (
		<div id="audit-query-error">
			<EmptyHero
				role="alert"
				className="rounded-none border-t border-line bg-transparent pt-12 pb-14"
				art={
					<ErrorReceipt
						className="w-[min(520px,100%)]"
						rows={[
							{
								label: "search",
								value: (
									<QueryErrorText
										error={rejected.error}
										highlightTo={rejected.end}
										className="[&_mark]:decoration-wavy [&_mark]:underline [&_mark]:underline-offset-3"
									/>
								),
							},
						]}
					/>
				}
				title={rejected.error.message}
				subtitle={
					auditQueryFix(
						Array.from(rejected.error.query).slice(rejected.start, rejected.end).join(""),
						rejected.error.message,
						rejected.fields,
					) ?? undefined
				}
			>
				<div className="flex flex-wrap justify-center gap-2">
					{rejected.fields.map((field) => (
						<Button
							key={field.key}
							variant="ghost"
							size="sm"
							title={field.label}
							className="h-7 font-mono text-[12px] font-normal"
							onClick={() => {
								onField(field.key);
							}}
						>
							@{field.key}:
						</Button>
					))}
				</div>
			</EmptyHero>
		</div>
	);
}
