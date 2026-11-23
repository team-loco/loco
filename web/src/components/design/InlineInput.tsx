import { PencilIcon } from "lucide-react";

import { cn } from "@/lib/utils";

/** A borderless text input that reads as text until hovered, with a pencil that hints it can be edited. */
export function InlineInput({
	className,
	wrapperClassName,
	...props
}: React.ComponentProps<"input"> & { wrapperClassName?: string }) {
	return (
		<span className={cn("group/inline relative inline-flex min-w-0 items-center gap-1.5", wrapperClassName)}>
			<input
				className={cn(
					"min-w-0 border-0 bg-transparent font-[inherit] outline-none hover:bg-panel2 focus:bg-panel2",
					className,
				)}
				{...props}
			/>
			<PencilIcon
				aria-hidden
				className="pointer-events-none size-[13px] shrink-0 text-fg3 opacity-0 transition-opacity duration-120 group-focus-within/inline:opacity-0 group-hover/inline:opacity-100"
			/>
		</span>
	);
}
