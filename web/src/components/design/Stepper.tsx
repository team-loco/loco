import { Button } from "./Button";

export function Stepper({
	value,
	onChange,
	min,
	max,
	label,
	disabled = false,
}: {
	value: number;
	onChange: (value: number) => void;
	min: number;
	max: number;
	label?: string;
	disabled?: boolean;
}) {
	return (
		<div role="group" className="flex h-[34px] w-fit items-center rounded-sm border border-line" aria-label={label}>
			<Button
				variant="ghost"
				className="h-full w-[34px] rounded-none px-0 text-[15px] text-fg2"
				disabled={disabled || value <= min}
				onClick={() => {
					onChange(Math.max(min, value - 1));
				}}
				aria-label="Decrease"
			>
				−
			</Button>
			<span className="w-[34px] text-center font-semibold tabular-nums">{value}</span>
			<Button
				variant="ghost"
				className="h-full w-[34px] rounded-none px-0 text-[15px] text-fg2"
				disabled={disabled || value >= max}
				onClick={() => {
					onChange(Math.min(max, value + 1));
				}}
				aria-label="Increase"
			>
				+
			</Button>
		</div>
	);
}
