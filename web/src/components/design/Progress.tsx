import { Progress as ProgressBase } from "@/components/ui/progress";
import { cn } from "@/lib/utils";

export function Progress({ className, ...props }: React.ComponentProps<typeof ProgressBase>) {
	return (
		<ProgressBase
			className={cn(
				"block [&_[data-slot=progress-track]]:h-2 [&_[data-slot=progress-track]]:bg-line2 [&_[data-slot=progress-indicator]]:rounded-full [&_[data-slot=progress-indicator]]:bg-ok-glyph",
				className,
			)}
			{...props}
		/>
	);
}
