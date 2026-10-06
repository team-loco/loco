import type { ReactNode } from "react";
import { ArrowRightIcon, CheckIcon, ChevronDownIcon, ChevronUpIcon, CopyIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { cn } from "@/lib/utils";

export function Dot({ className, color, size = 7 }: { className?: string; color?: string; size?: number }) {
	return (
		<span
			className={cn("shrink-0 rounded-[2px]", className)}
			style={{ width: size, height: size, ...(color !== undefined ? { background: color } : {}) }}
		/>
	);
}

interface HistoSegment {
	value: number;
	className: string;
}

export interface HistoBucket {
	segments: HistoSegment[];
	title: string;
}

export function Histogram({
	buckets,
	from,
	height,
	className,
}: {
	buckets: HistoBucket[];
	from: string;
	height: number;
	className?: string;
}) {
	const max = Math.max(1, ...buckets.map((b) => b.segments.reduce((a, s) => a + s.value, 0)));
	return (
		<div className={className}>
			<div className="flex items-end gap-0.5 px-4 pt-2.5" style={{ height }}>
				{buckets.map((b, i) => (
					<div
						key={String(i)}
						title={b.title}
						className="flex h-full flex-1 flex-col justify-end hover:opacity-75"
					>
						{b.segments.map((s, j) => (
							<div
								key={String(j)}
								className={cn(s.className, j === 0 && "rounded-t-[1px]")}
								style={{ height: `${String((s.value / max) * 100)}%` }}
							/>
						))}
					</div>
				))}
			</div>
			<div className="flex justify-between px-4 pt-1 pb-2.5 text-xs text-fg4">
				<span>{from}</span>
				<span>now</span>
			</div>
		</div>
	);
}

export function CopyButton({
	copied,
	onCopy,
	title = "Copy",
	className,
}: {
	copied: boolean;
	onCopy: () => void;
	title?: string;
	className?: string;
}) {
	return (
		<Button
			variant="ghost"
			size="icon-xs"
			title={title}
			onClick={(e) => {
				e.stopPropagation();
				onCopy();
			}}
			className={cn("size-[22px] text-fg4 hover:text-foreground", copied && "text-ok-fg hover:text-ok-fg", className)}
		>
			{copied ? <CheckIcon className="size-3" /> : <CopyIcon className="size-3" />}
		</Button>
	);
}

export function JumpButton({
	icon,
	label,
	hint,
	onClick,
	soon,
}: {
	icon: ReactNode;
	label: ReactNode;
	hint?: ReactNode;
	onClick?: (() => void) | undefined;
	soon?: boolean;
}) {
	const disabled = soon === true || onClick === undefined;
	return (
		<button
			type="button"
			disabled={disabled}
			onClick={onClick}
			className={cn(
				"flex h-[34px] w-full items-center gap-2 rounded-sm border border-line bg-background px-2.5 text-left text-foreground",
				disabled ? "cursor-not-allowed text-fg4" : "cursor-pointer hover:border-fg4 hover:bg-bg2",
			)}
		>
			<span className={cn("flex [&_svg]:size-[15px]", disabled ? "text-fg4" : "text-fg3")}>{icon}</span>
			<span className="min-w-0 flex-1 truncate">{label}</span>
			{soon === true && <SoonTag />}
			{hint !== undefined && <span className="text-sm whitespace-nowrap text-fg3">{hint}</span>}
			<ArrowRightIcon className="size-3.5 text-fg4" />
		</button>
	);
}

export function PanelNav({
	onPrev,
	onNext,
	onClose,
}: {
	onPrev: (() => void) | null;
	onNext: (() => void) | null;
	onClose: () => void;
}) {
	return (
		<>
			<Button
				variant="ghost"
				size="icon-sm"
				title="Newer"
				disabled={onPrev === null}
				onClick={onPrev ?? undefined}
				className="text-fg3 hover:text-foreground disabled:opacity-35"
			>
				<ChevronUpIcon className="size-[15px]" />
			</Button>
			<Button
				variant="ghost"
				size="icon-sm"
				title="Older"
				disabled={onNext === null}
				onClick={onNext ?? undefined}
				className="text-fg3 hover:text-foreground disabled:opacity-35"
			>
				<ChevronDownIcon className="size-[15px]" />
			</Button>
			<Button variant="ghost" size="icon-sm" title="Close" onClick={onClose} className="text-fg3 hover:text-foreground">
				<XIcon className="size-3.5" />
			</Button>
		</>
	);
}

export const PAGE_SIZES = [25, 50, 100];

export function DetailPanel({ header, children }: { header: ReactNode; children: ReactNode }) {
	return (
		<aside className="sticky top-4 flex max-h-[calc(100vh-32px)] min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-background">
			<div className="flex items-center gap-2 border-b border-line py-2.5 pr-3 pl-3.5">{header}</div>
			{children}
		</aside>
	);
}
