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
		<div className="flex flex-col items-center gap-3.5 px-6 py-14 text-center">
			{icon !== undefined && (
				<span className="flex size-10 items-center justify-center rounded-lg border border-line bg-bg2 text-fg3 [&_svg]:size-[18px]">
					{icon}
				</span>
			)}
			<div className="flex max-w-full flex-col items-center gap-2">
				<span className="text-[14.5px] font-semibold">{title}</span>
				{query !== undefined && query !== "" && (
					<span className="max-w-[520px] truncate rounded-sm border border-line bg-bg2 px-2 py-0.5 font-mono text-sm text-fg2">
						{query}
					</span>
				)}
				{children !== undefined && <span className="max-w-sm text-fg3">{children}</span>}
			</div>
			{action !== undefined && <div className="flex flex-wrap justify-center gap-2">{action}</div>}
		</div>
	);
}
