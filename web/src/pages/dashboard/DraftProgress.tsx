import { StepList } from "@/components/design/StepList";
import type { StepState } from "@/lib/deploy-progress";

export function DraftProgress({
	title,
	elapsed,
	steps,
}: {
	title: string;
	elapsed: string;
	steps: { label: string; state: StepState; meta: string }[];
}) {
	return (
		<div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-6 py-5">
			<div className="flex items-center justify-between">
				<span className="font-semibold">{title}</span>
				<span className="text-fg3 tabular-nums">{elapsed}</span>
			</div>
			<StepList steps={steps} />
		</div>
	);
}
