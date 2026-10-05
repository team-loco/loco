import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Field({
	label,
	hint,
	error,
	strong,
	className,
	children,
}: {
	label: ReactNode;
	hint?: ReactNode;
	error?: ReactNode;
	strong?: boolean;
	className?: string;
	children: ReactNode;
}) {
	return (
		<label className={cn("flex min-w-0 flex-col gap-1.5", className)}>
			<span className={strong === true ? "font-semibold" : "text-sm text-fg3"}>{label}</span>
			{children}
			{error !== undefined && error !== null && error !== false ? (
				<span className="text-sm text-bad-fg">{error}</span>
			) : (
				hint !== undefined && <span className="text-sm text-fg3">{hint}</span>
			)}
		</label>
	);
}
