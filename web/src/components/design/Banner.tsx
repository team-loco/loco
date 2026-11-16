import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function Banner({
	title,
	children,
	tone = "bad",
	action,
	className,
}: {
	title: string;
	children: ReactNode;
	tone?: "bad" | "warn";
	action?: ReactNode;
	className?: string;
}) {
	return (
		<div
			role="status"
			className={cn(
				"mx-[clamp(16px,3vw,28px)] mt-4 flex items-start gap-2.5 rounded-card px-4 py-3 text-[13px]",
				tone === "warn" ? "border border-warn/40 bg-warn-bg text-foreground" : "bg-bad-bg text-bad-fg",
				className,
			)}
		>
			<TriangleAlertIcon aria-hidden className={cn("size-[1lh] shrink-0", tone === "warn" && "mt-1 text-warn")} />
			<div className="min-w-0 flex-1">
				<span className="font-medium">{title}</span> {children}
			</div>
			{action}
		</div>
	);
}
