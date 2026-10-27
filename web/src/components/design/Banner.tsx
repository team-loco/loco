import { TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

export function Banner({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div
			role="status"
			className="flex items-start gap-2.5 border-b border-line bg-bad-bg px-5 py-2.5 text-base text-bad-fg"
		>
			<TriangleAlertIcon aria-hidden className="size-[1lh] shrink-0" />
			<p className="min-w-0">
				<span className="font-medium">{title}</span> {children}
			</p>
		</div>
	);
}
