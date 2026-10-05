import { Slider as SliderBase } from "@/components/ui/slider"
import { cn } from "@/lib/utils"

function Slider({ className, ...props }: React.ComponentProps<typeof SliderBase>) {
	return (
		<SliderBase
			className={cn(
				"[&_[data-slot=slider-range]]:bg-primary [&_[data-slot=slider-thumb]]:size-5 [&_[data-slot=slider-thumb]]:border-2 [&_[data-slot=slider-thumb]]:border-primary [&_[data-slot=slider-thumb]]:bg-background [&_[data-slot=slider-thumb]]:shadow-[0_1px_3px_rgba(0,0,0,0.18)] [&_[data-slot=slider-track]]:bg-bg3",
				className
			)}
			{...props}
		/>
	)
}

export { Slider }
