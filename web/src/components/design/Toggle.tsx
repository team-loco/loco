import { Toggle as ToggleBase } from "@/components/ui/toggle";
import { cn } from "@/lib/utils";

export function Toggle({ className, ...props }: React.ComponentProps<typeof ToggleBase>) {
	return (
		<ToggleBase
			className={cn(
				"h-8 gap-1.5 rounded-control border-line2 bg-raise px-[13px] text-[13px] font-normal text-foreground hover:border-fg4 hover:bg-panel2 focus-visible:ring-2 disabled:opacity-45 aria-pressed:bg-panel2 [&_svg:not([class*='size-'])]:size-3.5",
				className,
			)}
			{...props}
		/>
	);
}
