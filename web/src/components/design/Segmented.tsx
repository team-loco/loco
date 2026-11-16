import { LayoutGroup, MotionConfig, motion } from "motion/react";
import { useId, type ReactNode } from "react";

export const segmentedGroupClass = "rounded-control bg-panel2 p-0.5 data-[variant=line]:rounded-control";
export const segmentedItemClass =
	"relative isolate h-[26px] gap-[7px] rounded-control-inner! border border-transparent px-2.5 text-[12.5px] font-normal text-fg2 after:hidden hover:bg-transparent hover:text-foreground aria-pressed:bg-transparent aria-pressed:text-foreground data-pressed:bg-transparent data-pressed:text-foreground data-active:bg-transparent data-active:text-foreground dark:data-active:border-transparent dark:data-active:bg-transparent dark:data-active:text-foreground";

export function SegmentedScope({ children }: { children: ReactNode }) {
	const id = useId();
	return (
		<MotionConfig reducedMotion="user">
			<LayoutGroup id={id}>{children}</LayoutGroup>
		</MotionConfig>
	);
}

export function SegmentedThumb({ value = "selection" }: { value?: string }) {
	return (
		<motion.span
			aria-hidden
			data-slot="segmented-indicator"
			layoutId={value}
			initial={false}
			transition={{ type: "tween", duration: 0.18, ease: "easeOut" }}
			className="pointer-events-none absolute -inset-px -z-10 rounded-control-inner bg-raise shadow-xs ring-1 ring-line dark:shadow-none dark:ring-line2"
		/>
	);
}
