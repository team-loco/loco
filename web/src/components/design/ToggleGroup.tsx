import { SegmentedScope, SegmentedThumb, segmentedGroupClass, segmentedItemClass, segmentedRoot } from "./Segmented";
import { createContext, use, useState, type ReactNode } from "react";

import { ToggleGroup as ToggleGroupBase, ToggleGroupItem as ToggleGroupItemBase } from "@/components/ui/toggle-group";
import { cn } from "@/lib/utils";

type ToggleGroupVariant = "default" | "outline" | "segmented";

const EMPTY_VALUES: readonly string[] = [];

const SegmentedContext = createContext({ segmented: false, values: EMPTY_VALUES, multiple: false });

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
	variant?: ToggleGroupVariant | undefined;
}) {
	const segmented = variant === "segmented";
	const [localValue, setLocalValue] = useState(defaultValue);
	const values = value ?? localValue;
	return (
		<SegmentedContext value={{ segmented, values, multiple: props.multiple ?? false }}>
			<SegmentedScope>
				<ToggleGroupBase
					data-segmented={segmented || undefined}
					render={segmented ? segmentedRoot : undefined}
					variant={segmented ? "default" : variant}
					spacing={segmented ? 0.5 : spacing}
					className={cn(segmented && segmentedGroupClass, className)}
					value={values}
					onValueChange={(next, details) => {
						if (value === undefined) setLocalValue(next);
						onValueChange?.(next, details);
					}}
					{...props}
				>
					{children}
				</ToggleGroupBase>
			</SegmentedScope>
		</SegmentedContext>
	);
}

function ToggleGroupItem({
	className,
	children,
	count,
	...props
}: React.ComponentProps<typeof ToggleGroupItemBase> & { count?: ReactNode }) {
	const { segmented, values, multiple } = use(SegmentedContext);
	const selected = props.value !== undefined && values.includes(props.value);
	return (
		<ToggleGroupItemBase
			className={cn(
				"cursor-pointer disabled:pointer-events-auto disabled:cursor-not-allowed data-disabled:pointer-events-auto data-disabled:cursor-not-allowed",
				segmented && segmentedItemClass,
				className,
			)}
			{...props}
		>
			{segmented && selected && props.value !== undefined && (
				<SegmentedThumb value={multiple ? props.value : "selection"} />
			)}
			{children}
			{count !== undefined && (
				<span
					data-slot="segmented-count"
					className="inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-sm bg-foreground/7 px-1 font-mono text-[10.5px] leading-none text-fg3 tabular-nums"
				>
					{count}
				</span>
			)}
		</ToggleGroupItemBase>
	);
}

export { ToggleGroup, ToggleGroupItem };
