import { Button } from "./Button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./Tooltip";

function unsavedLabel(count: number | undefined): string {
	if (count === undefined) return "Unsaved changes";
	return `${count.toString()} unsaved ${count === 1 ? "change" : "changes"}`;
}

function SaveBar({
	count,
	saveLabel = "Save",
	onDiscard,
	onSave,
	saving,
	disabledReason,
}: {
	count?: number | undefined;
	saveLabel?: string | undefined;
	onDiscard: () => void;
	onSave: () => void;
	saving: boolean;
	disabledReason: string | null;
}) {
	const save = (
		<Button
			className="bg-primary-foreground text-primary hover:bg-primary-foreground"
			disabled={disabledReason !== null || saving}
			onClick={onSave}
		>
			{saving ? "Saving…" : saveLabel}
		</Button>
	);
	return (
		<div className="sticky bottom-4 z-20 flex animate-in items-center gap-3 rounded-card bg-primary py-2.5 pr-2.5 pl-4 text-[13px] text-primary-foreground shadow-bar duration-240 fade-in-0 slide-in-from-bottom-2 motion-reduce:animate-none">
			<span className="flex-1">{unsavedLabel(count)}</span>
			<Button
				variant="ghost"
				size="sm"
				className="border-primary-foreground/30 text-primary-foreground hover:bg-primary-foreground/10 hover:text-primary-foreground dark:hover:bg-primary-foreground/10"
				disabled={saving}
				onClick={onDiscard}
			>
				Discard
			</Button>
			{disabledReason === null ? (
				save
			) : (
				<Tooltip>
					<TooltipTrigger render={<span className="inline-flex" />}>{save}</TooltipTrigger>
					<TooltipContent>{disabledReason}</TooltipContent>
				</Tooltip>
			)}
		</div>
	);
}

export { SaveBar };
