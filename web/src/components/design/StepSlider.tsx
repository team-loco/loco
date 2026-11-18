import { Slider as SliderPrimitive } from "@base-ui/react/slider";

import "./Slider.css";

export function stepLabel(options: readonly { value: string; label: string }[], value: string): string {
	return options.find((o) => o.value === value)?.label ?? value;
}

export function SliderValue({ text }: { text: string }) {
	return <span className="font-mono text-[12.5px] text-foreground">{text}</span>;
}

// Built on the Base UI primitive because the stock shadcn slider cannot label its thumb.
export function StepSlider({
	label,
	options,
	value,
	onChange,
	disabled = false,
	describedBy,
}: {
	label: string;
	options: readonly { value: string; label: string }[];
	value: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	describedBy?: string | undefined;
}) {
	const index = options.findIndex((o) => o.value === value);
	const current = options[index];
	return (
		<div className="flex flex-col gap-2">
			<SliderPrimitive.Root
				min={0}
				max={Math.max(0, options.length - 1)}
				step={1}
				value={Math.max(0, index)}
				disabled={disabled || options.length < 2}
				thumbAlignment="center"
				onValueChange={(next: number | readonly number[]) => {
					const i: number | undefined = typeof next === "number" ? next : next[0];
					const picked = i === undefined ? undefined : options[i];
					if (picked !== undefined && picked.value !== value) onChange(picked.value);
				}}
			>
				<SliderPrimitive.Control className="dsl-box data-disabled:opacity-50">
					<SliderPrimitive.Track className="dsl-rail">
						<SliderPrimitive.Indicator className="dsl-fill" />
						{options.map((option, i) => (
							<span
								key={option.value}
								aria-hidden
								className="dsl-stop"
								data-on={i <= index ? "" : undefined}
								style={{ left: `${(i / Math.max(1, options.length - 1)) * 100}%` }}
							/>
						))}
					</SliderPrimitive.Track>
					<SliderPrimitive.Thumb
						aria-label={label}
						aria-describedby={describedBy}
						getAriaValueText={() => current?.label ?? value}
						className="dsl-thumb"
					/>
				</SliderPrimitive.Control>
			</SliderPrimitive.Root>
			<span className="dsl-labels">
				{options.map((o, i) => (
					<span
						key={o.value}
						data-on={i === index ? "" : undefined}
						style={{ left: `${(i / Math.max(1, options.length - 1)) * 100}%` }}
					>
						{o.label}
					</span>
				))}
			</span>
		</div>
	);
}
