import type { ReactNode } from "react";

export function EmptyState({
	icon,
	title,
	children,
	action,
}: {
	icon?: ReactNode;
	title: ReactNode;
	children?: ReactNode;
	action?: ReactNode;
}) {
	return (
		<div className="flex flex-col items-center gap-2 px-4 py-12 text-center">
			{icon !== undefined && (
				<span className="mb-1 flex size-10 items-center justify-center rounded-xl border border-dashed border-line2 bg-bg3 text-fg2 [&_svg]:size-[18px]">
					{icon}
				</span>
			)}
			<span className="font-semibold">{title}</span>
			{children !== undefined && <span className="max-w-sm text-fg3">{children}</span>}
			{action !== undefined && <div className="mt-2">{action}</div>}
		</div>
	);
}
