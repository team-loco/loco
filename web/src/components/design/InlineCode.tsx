import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Code, commands and names (a workspace, an environment) inside running text. */
export function InlineCode({ children, className }: { children: ReactNode; className?: string }) {
	return (
		<code
			className={cn(
				"inline-block rounded-[4px] bg-panel2 px-1.5 font-mono text-[0.86em] leading-[1.4] whitespace-nowrap text-foreground",
				className,
			)}
		>
			{children}
		</code>
	);
}
