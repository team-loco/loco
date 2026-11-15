import { Kbd as KbdBase } from "@/components/ui/kbd";
import { cn } from "@/lib/utils";

function Kbd({ className, ...props }: React.ComponentProps<typeof KbdBase>) {
	return (
		<KbdBase
			className={cn(
				// Tooltips are light surfaces here; undo the vendored inverted tooltip colours.
				"h-auto min-w-0 rounded-sm bg-panel px-[5px] py-px font-mono text-[10.5px] font-normal text-fg2 inset-ring inset-ring-line2 in-data-[slot=tooltip-content]:bg-panel in-data-[slot=tooltip-content]:text-fg2 dark:in-data-[slot=tooltip-content]:bg-panel",
				className,
			)}
			{...props}
		/>
	);
}

export { Kbd };
