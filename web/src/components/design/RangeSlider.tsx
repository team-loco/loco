import { useId, type ReactNode } from "react";

import { Slider as SliderBase } from "@/components/ui/slider";
import { cn } from "@/lib/utils";
import "./Slider.css";

/** A percentage slider with its heading and value on one row above it. */
export function RangeSlider({
	label,
	heading,
	value,
	min,
	max,
	disabled,
	onChange,
}: {
	label: string;
	heading: ReactNode;
	value: number;
	min: number;
	max: number;
	disabled?: boolean;
	onChange: (value: number) => void;
}) {
	const labelId = useId();
	return (
		<div className={cn("flex min-w-0 flex-col gap-2", disabled === true && "opacity-60")}>
			<span className="flex items-center justify-between gap-2 text-[13px] font-medium">
				{heading}
				<span className="font-mono font-normal text-fg2">{value}%</span>
			</span>
			<span id={labelId} className="sr-only">
				{label}
			</span>
			<SliderBase
				className="range"
				aria-labelledby={labelId}
				value={[value]}
				min={min}
				max={max}
				disabled={disabled}
				format={{ style: "unit", unit: "percent" }}
				onValueChange={(next) => {
					onChange(typeof next === "number" ? next : next[0]!);
				}}
			/>
		</div>
	);
}
