import { CircleCheckIcon, CircleIcon, CircleXIcon, LoaderCircleIcon } from "lucide-react";

import type { StepState } from "@/lib/deploy-progress";
import { cn } from "@/lib/utils";

function StepIcon({ state }: { state: StepState }) {
	switch (state) {
		case "done":
			return <CircleCheckIcon className="size-4 text-ok-fg" />;
		case "active":
			return <LoaderCircleIcon className="size-4 animate-spin text-info-fg" />;
		case "failed":
			return <CircleXIcon className="size-4 text-bad-fg" />;
		case "todo":
			return <CircleIcon className="size-4 text-fg4" />;
	}
}

export function StepList({
	steps,
	className,
}: {
	steps: { label: string; state: StepState; meta: string }[];
	className?: string;
}) {
	return (
		<ol className={cn("m-0 flex list-none flex-col p-0", className)}>
			{steps.map((s) => {
				const shown = s.state !== "todo";
				return (
					<li key={s.label} className={cn("grid grid-cols-[20px_minmax(0,1fr)] gap-3 py-2.5", !shown && "opacity-45")}>
						<span className="flex pt-px">
							<StepIcon state={s.state} />
						</span>
						<div className="flex min-w-0 flex-col gap-0.5">
							<span className={s.state === "active" ? "font-semibold" : undefined}>{s.label}</span>
							<span className="text-sm break-words text-fg3">{shown ? s.meta : ""}</span>
						</div>
					</li>
				);
			})}
		</ol>
	);
}
