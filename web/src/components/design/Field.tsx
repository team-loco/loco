import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Field({
	label,
	hint,
	error,
	className,
	children,
}: {
	label: ReactNode;
	hint?: ReactNode;
	error?: ReactNode;
	className?: string;
	children: ReactNode;
}) {
	return (
		<label className={cn("flex min-w-0 flex-col gap-1.5", className)}>
			<span className="text-[13px] font-medium text-foreground">{label}</span>
			{children}
			{error !== undefined && error !== null && error !== false ? (
				<span className="text-[12.5px] text-bad-fg">{error}</span>
			) : (
				hint !== undefined && <span className="text-[12.5px] text-fg3">{hint}</span>
			)}
		</label>
	);
}
