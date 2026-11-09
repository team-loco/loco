import type { ReactNode } from "react";

export function EmptyState({
	icon,
	title,
	query,
	children,
	action,
}: {
	icon?: ReactNode;
	title: ReactNode;
	query?: string | undefined;
	children?: ReactNode;
	action?: ReactNode;
}) {
	return (
		<div className="flex flex-col items-center gap-3 px-6 py-7 text-center text-[13px]">
			{icon !== undefined && (
				<span className="mb-1 flex size-12 items-center justify-center rounded-[12px] bg-background text-fg2 ring-1 ring-line [&_svg]:size-5">
					{icon}
				</span>
			)}
			<div className="flex max-w-full flex-col items-center gap-2">
				<span className="text-[13.5px] font-medium">{title}</span>
				{query !== undefined && query !== "" && (
					<span className="max-w-[520px] truncate rounded-sm bg-panel2 px-2 py-0.5 font-mono text-[12px] text-fg2">
						{query}
					</span>
				)}
				{children !== undefined && <span className="max-w-sm text-fg3">{children}</span>}
			</div>
			{action !== undefined && <div className="flex flex-wrap justify-center gap-2">{action}</div>}
		</div>
	);
}
