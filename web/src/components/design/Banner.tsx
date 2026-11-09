import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

export function Banner({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div
			role="status"
			className="mx-[clamp(16px,3vw,28px)] mt-4 flex items-start gap-2.5 rounded-[10px] bg-bad-bg px-4 py-3 text-[13px] text-bad-fg"
		>
			<TriangleAlertIcon aria-hidden className="size-[1lh] shrink-0" />
			<p className="min-w-0">
				<span className="font-medium">{title}</span> {children}
			</p>
		</div>
	);
}
