import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Wrapping line of facts spaced apart. */
export function MetaLine({ className, children }: { className?: string; children: ReactNode }) {
	return (
		<span data-meta-line className={cn("flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1", className)}>
			{children}
		</span>
	);
}
