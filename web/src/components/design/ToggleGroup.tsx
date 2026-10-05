import { createContext, use } from "react"

import {
	ToggleGroup as ToggleGroupBase,
	ToggleGroupItem as ToggleGroupItemBase,
} from "@/components/ui/toggle-group"
import { cn } from "@/lib/utils"

type ToggleGroupVariant = "default" | "outline" | "segmented"

const SegmentedContext = createContext(false)

function ToggleGroup({
	className,
	variant = "default",
	spacing = 2,
	...props
}: Omit<React.ComponentProps<typeof ToggleGroupBase>, "variant"> & {
	variant?: ToggleGroupVariant | undefined
}) {
	const segmented = variant === "segmented"
	return (
		<SegmentedContext value={segmented}>
			<ToggleGroupBase
				variant={segmented ? "default" : variant}
				spacing={segmented ? 0.5 : spacing}
				className={cn(segmented && "rounded-lg bg-bg3 p-[3px]", className)}
				{...props}
			/>
		</SegmentedContext>
	)
}

function ToggleGroupItem({ className, ...props }: React.ComponentProps<typeof ToggleGroupItemBase>) {
	const segmented = use(SegmentedContext)
	return (
		<ToggleGroupItemBase
			className={cn(
				segmented &&
					"h-[30px] rounded-md! border border-transparent px-2.5 text-base font-medium text-fg3 hover:bg-transparent hover:text-foreground aria-pressed:border-line aria-pressed:bg-background aria-pressed:text-foreground aria-pressed:shadow-[0_1px_2px_rgba(0,0,0,0.08)] data-pressed:border-line data-pressed:bg-background data-pressed:text-foreground data-pressed:shadow-[0_1px_2px_rgba(0,0,0,0.08)]",
				className
			)}
			{...props}
		/>
	)
}

export { ToggleGroup, ToggleGroupItem }
