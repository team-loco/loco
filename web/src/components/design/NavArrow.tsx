import { cn } from "@/lib/utils";

/** The "→" a navigating element shows on hover or keyboard focus; the element carries the `group/nav` class. */
export function NavArrow({ className }: { className?: string }) {
	return (
		<span
			aria-hidden="true"
			data-slot="nav-arrow"
			className={cn(
				"ml-1.5 shrink-0 -translate-x-1 text-fg3 opacity-0 transition-[opacity,translate] duration-150 group-hover/nav:translate-x-0 group-hover/nav:opacity-100 group-focus-visible/nav:translate-x-0 group-focus-visible/nav:opacity-100",
				className,
			)}
		>
			→
		</span>
	);
}
