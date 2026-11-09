import { cn } from "@/lib/utils";

import { Button } from "./Button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./Tooltip";

function DangerZone({
	title,
	description,
	warn,
	actionLabel,
	blockedReason,
	onAction,
}: {
	title: string;
	description?: React.ReactNode;
	warn?: boolean | undefined;
	actionLabel: string;
	blockedReason: string | null;
	onAction: () => void;
}) {
	const button = (
		<Button
			variant="destructive-outline"
			size="sm"
			className="max-w-full min-w-0"
			disabled={blockedReason !== null}
			onClick={onAction}
		>
			<span className="truncate">{actionLabel}</span>
		</Button>
	);
	return (
		<section className="flex flex-wrap items-center justify-between gap-4 rounded-card p-4 shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--err)_35%,transparent)]">
			<div className="flex min-w-0 flex-1 basis-60 flex-col gap-0.5">
				<span className="text-[13.5px] font-medium">{title}</span>
				{description !== undefined && (
					<span className={cn("text-[12.5px]", warn === true ? "text-warn-fg" : "text-fg3")}>{description}</span>
				)}
			</div>
			{blockedReason === null ? (
				button
			) : (
				<Tooltip>
					<TooltipTrigger render={<span className="inline-flex max-w-full" />}>{button}</TooltipTrigger>
					<TooltipContent>{blockedReason}</TooltipContent>
				</Tooltip>
			)}
		</section>
	);
}

export { DangerZone };
