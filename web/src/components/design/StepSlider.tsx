import { Slider as SliderPrimitive } from "@base-ui/react/slider";

import { cn } from "@/lib/utils";

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
			<span className="font-mono text-[13px] text-foreground">{current?.label ?? value}</span>
			<SliderPrimitive.Root
				min={0}
				max={Math.max(0, options.length - 1)}
				step={1}
				value={Math.max(0, index)}
				disabled={disabled || options.length < 2}
				thumbAlignment="edge"
				onValueChange={(next: number | readonly number[]) => {
					const i: number | undefined = typeof next === "number" ? next : next[0];
					const picked = i === undefined ? undefined : options[i];
					if (picked !== undefined && picked.value !== value) onChange(picked.value);
				}}
			>
				<SliderPrimitive.Control className="relative flex w-full touch-none items-center py-1.5 select-none data-disabled:opacity-50">
					<SliderPrimitive.Track className="relative h-1 grow overflow-hidden rounded-full bg-line2">
						<SliderPrimitive.Indicator className="h-full bg-foreground" />
					</SliderPrimitive.Track>
					<SliderPrimitive.Thumb
						aria-label={label}
						aria-describedby={describedBy}
						getAriaValueText={() => current?.label ?? value}
						className="block size-3.5 rounded-full border border-foreground bg-background ring-ring/50 focus-visible:ring-3 focus-visible:outline-hidden"
					/>
				</SliderPrimitive.Control>
			</SliderPrimitive.Root>
			<span className="flex justify-between font-mono text-[11px] text-fg3">
				{options.map((o, i) => (
					<span key={o.value} className={cn(i === index && "text-foreground")}>
						{o.label}
					</span>
				))}
			</span>
		</div>
	);
}
