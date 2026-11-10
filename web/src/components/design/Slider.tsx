import { Slider as SliderPrimitive } from "@base-ui/react/slider";
import type { ReactNode } from "react";

export function HistogramSlider({
	label,
	valueText,
	children,
	onActivate,
	...props
}: SliderPrimitive.Root.Props<number> & {
	label: string;
	valueText: string;
	children: ReactNode;
	onActivate: () => void;
}) {
	return (
		<SliderPrimitive.Root {...props}>
			<SliderPrimitive.Control onClick={onActivate} className="relative h-[72px] w-full touch-none select-none">
				<SliderPrimitive.Track className="flex h-full items-end gap-0.5">{children}</SliderPrimitive.Track>
				<SliderPrimitive.Thumb
					aria-label={label}
					aria-valuetext={valueText}
					onKeyDown={(event) => {
						if (event.key === "Enter") {
							event.preventDefault();
							onActivate();
						}
					}}
					className="absolute top-0 h-full w-0.5 rounded-sm outline-none focus-within:bg-foreground focus-within:ring-2 focus-within:ring-ring"
				/>
			</SliderPrimitive.Control>
		</SliderPrimitive.Root>
	);
}
