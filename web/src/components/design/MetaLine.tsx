import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Wrapping line of facts with a "·" after each fact but the last. */
export function MetaLine({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<span
			data-meta-line
			className={cn(
				"flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 [&>*:not(:last-child)]:after:ml-2 [&>*:not(:last-child)]:after:content-['·']",
				className,
			)}
		>
			{children}
		</span>
	);
}
