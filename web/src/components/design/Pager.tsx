import { ChevronLeftIcon, ChevronRightIcon, ChevronsLeftIcon, ChevronsRightIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

function PagerButton({
	label,
	onClick,
	children,
}: {
	label: string;
	onClick: (() => void) | null;
	children: ReactNode;
}) {
	return (
		<Button
			variant="outline"
			size="icon"
			aria-label={label}
			title={label}
			disabled={onClick === null}
			onClick={onClick ?? undefined}
		>
			{children}
		</Button>
	);
}

function Pager({
	label,
	pageSizes,
	pageSize,
	onPageSize,
	prevLabel = "Previous page",
	nextLabel = "Next page",
	firstLabel = "First page",
	lastLabel = "Last page",
	onFirst,
	onPrev,
	onNext,
	onLast,
	className,
}: {
	label: ReactNode;
	pageSizes?: readonly number[] | undefined;
	pageSize?: number | undefined;
	onPageSize?: ((size: number) => void) | undefined;
	prevLabel?: string | undefined;
	nextLabel?: string | undefined;
	firstLabel?: string | undefined;
	lastLabel?: string | undefined;
	onFirst?: (() => void) | null | undefined;
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onLast?: (() => void) | null | undefined;
	className?: string | undefined;
}) {
	const sizes = pageSizes !== undefined && pageSize !== undefined && onPageSize !== undefined;
	return (
		<div
			className={cn(
				"flex flex-wrap items-center gap-2 border-t border-line2 px-5 pt-3 pb-3.5 text-[12.5px] text-fg2",
				className,
			)}
		>
			<span className="font-mono tabular-nums">{label}</span>
			<span className="flex-1" />
			{sizes && (
				<span className="flex items-center gap-2 text-fg3">
					Rows per page
					<ToggleGroup
						variant="segmented"
						aria-label="Rows per page"
						value={[String(pageSize)]}
						onValueChange={(v: string[]) => {
							const n = Number(v[0]);
							if (Number.isFinite(n) && n > 0) onPageSize(n);
						}}
					>
						{pageSizes.map((n) => (
							<ToggleGroupItem key={n} value={String(n)} className="h-6 px-2 font-mono text-[12px]">
								{n}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				</span>
			)}
			<span className="flex gap-1">
				{onFirst !== undefined && (
					<PagerButton label={firstLabel} onClick={onFirst}>
						<ChevronsLeftIcon />
					</PagerButton>
				)}
				<PagerButton label={prevLabel} onClick={onPrev}>
					<ChevronLeftIcon />
				</PagerButton>
				<PagerButton label={nextLabel} onClick={onNext}>
					<ChevronRightIcon />
				</PagerButton>
				{onLast !== undefined && (
					<PagerButton label={lastLabel} onClick={onLast}>
						<ChevronsRightIcon />
					</PagerButton>
				)}
			</span>
		</div>
	);
}

export { Pager };
