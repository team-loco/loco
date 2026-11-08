import { Kbd as KbdBase } from "@/components/ui/kbd";
import { cn } from "@/lib/utils";

function Kbd({ className, ...props }: React.ComponentProps<typeof KbdBase>) {
	return (
		<KbdBase
			className={cn(
				"h-auto min-w-0 rounded-sm bg-panel px-[5px] py-px font-mono text-[10.5px] font-normal text-fg2 inset-ring inset-ring-line2",
				className,
			)}
			{...props}
		/>
	);
}

export { Kbd };
