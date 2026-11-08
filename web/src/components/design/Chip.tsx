import { XIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Chip({
	children,
	mono = false,
	onRemove,
	removeLabel,
	className,
}: {
	children: ReactNode;
	mono?: boolean | undefined;
	onRemove?: (() => void) | undefined;
	removeLabel?: string | undefined;
	className?: string | undefined;
}) {
	return (
		<span
			className={cn(
				"inline-flex h-6 items-center gap-1.5 rounded-md bg-panel2 px-2 text-[12px] whitespace-nowrap text-fg2",
				mono && "font-mono text-[11.5px]",
				className,
			)}
		>
			{children}
			{onRemove !== undefined && (
				<button
					type="button"
					aria-label={removeLabel ?? "Remove"}
					onClick={onRemove}
					className="flex rounded-sm text-fg3 hover:text-foreground focus-visible:outline-2 focus-visible:outline-fg4"
				>
					<XIcon className="size-3" />
				</button>
			)}
		</span>
	);
}
