import { LayoutGroup, MotionConfig, motion } from "motion/react"
import { createContext, use, useId, useState } from "react"

import {
	ToggleGroup as ToggleGroupBase,
	ToggleGroupItem as ToggleGroupItemBase,
} from "@/components/ui/toggle-group"
import { cn } from "@/lib/utils"

type ToggleGroupVariant = "default" | "outline" | "segmented"

const EMPTY_VALUES: readonly string[] = []

const SegmentedContext = createContext({ segmented: false, values: EMPTY_VALUES, multiple: false })

function ToggleGroup({
	className,
	variant = "default",
	spacing = 2,
	value,
	defaultValue = EMPTY_VALUES,
	onValueChange,
	children,
	...props
}: Omit<React.ComponentProps<typeof ToggleGroupBase>, "variant"> & {
	variant?: ToggleGroupVariant | undefined
}) {
	const segmented = variant === "segmented"
	const id = useId()
	const [localValue, setLocalValue] = useState(defaultValue)
	const values = value ?? localValue
	return (
		<SegmentedContext value={{ segmented, values, multiple: props.multiple ?? false }}>
			<MotionConfig reducedMotion="user">
				<LayoutGroup id={id}>
					<ToggleGroupBase
						variant={segmented ? "default" : variant}
						spacing={segmented ? 0.5 : spacing}
						className={cn(segmented && "rounded-md bg-bg3 p-0.5", className)}
						value={values}
						onValueChange={(next, details) => {
							if (value === undefined) setLocalValue(next)
							onValueChange?.(next, details)
						}}
						{...props}
					>
						{children}
					</ToggleGroupBase>
				</LayoutGroup>
			</MotionConfig>
		</SegmentedContext>
	)
}

function ToggleGroupItem({ className, children, ...props }: React.ComponentProps<typeof ToggleGroupItemBase>) {
	const { segmented, values, multiple } = use(SegmentedContext)
	const selected = props.value !== undefined && values.includes(props.value)
	return (
		<ToggleGroupItemBase
			className={cn(
				"cursor-pointer disabled:pointer-events-auto disabled:cursor-not-allowed data-disabled:pointer-events-auto data-disabled:cursor-not-allowed",
				segmented &&
					"relative isolate h-7 rounded-sm! border border-transparent px-2.5 text-sm font-medium text-fg3 hover:bg-transparent hover:text-foreground aria-pressed:bg-transparent aria-pressed:text-foreground data-pressed:bg-transparent data-pressed:text-foreground",
				className
			)}
			{...props}
		>
			{segmented && selected && props.value !== undefined && (
				<motion.span
					aria-hidden
					data-slot="segmented-indicator"
					layoutId={multiple ? props.value : "selection"}
					initial={false}
					transition={{ type: "tween", duration: 0.18, ease: "easeOut" }}
					className="pointer-events-none absolute -inset-px -z-10 rounded-sm border border-line bg-background shadow-xs"
				/>
			)}
			{children}
		</ToggleGroupItemBase>
	)
}

export { ToggleGroup, ToggleGroupItem }
