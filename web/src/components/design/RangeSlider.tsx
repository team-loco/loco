import { useId } from "react";

import { Slider as SliderBase } from "@/components/ui/slider";
import "./Slider.css";

export function RangeSlider({
	label,
	value,
	min,
	max,
	disabled,
	onChange,
}: {
	label: string;
	value: number;
	min: number;
	max: number;
	disabled?: boolean;
	onChange: (value: number) => void;
}) {
	const labelId = useId();
	return (
		<div className="flex max-w-[420px] flex-col gap-2">
			<span className="font-mono text-[13px]">{value}%</span>
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
