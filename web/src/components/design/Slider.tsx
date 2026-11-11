import { Slider as SliderBase } from "@/components/ui/slider";
import { cn } from "@/lib/utils";

// A full-height, transparent slider laid over a histogram: the bars show the data and
// the thumb is a thin cursor that appears on keyboard focus.
export function HistogramSlider({ className, ...props }: React.ComponentProps<typeof SliderBase>) {
	return (
		<SliderBase
			className={cn(
				"h-full *:h-full [&_[data-slot=slider-range]]:hidden [&_[data-slot=slider-thumb]]:h-full! [&_[data-slot=slider-thumb]]:w-0.5! [&_[data-slot=slider-thumb]]:rounded-sm! [&_[data-slot=slider-thumb]]:border-0 [&_[data-slot=slider-thumb]]:bg-transparent [&_[data-slot=slider-thumb]]:ring-0! [&_[data-slot=slider-thumb]]:after:hidden [&_[data-slot=slider-thumb]]:has-focus-visible:bg-foreground [&_[data-slot=slider-track]]:h-full! [&_[data-slot=slider-track]]:bg-transparent",
				className,
			)}
			{...props}
		/>
	);
}
