import { useState, type ReactNode } from "react";
import { ArrowRightIcon, CheckIcon, ChevronDownIcon, ChevronUpIcon, CopyIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import type { Paging } from "@/lib/paging";
import { cn } from "@/lib/utils";

export function Dot({ className, color, size = 7 }: { className?: string; color?: string; size?: number }) {
	return (
		<span
			className={cn("shrink-0 rounded-[2px]", className)}
			style={{ width: size, height: size, ...(color !== undefined ? { background: color } : {}) }}
		/>
	);
}

export interface VolumeSegment {
	value: number;
	color: string;
}

export interface VolumeBucket {
	label: string;
	segments: VolumeSegment[];
}

export function VolumeBars({
	buckets,
	label,
	onPick,
}: {
	buckets: VolumeBucket[];
	label: string;
	onPick: (index: number) => void;
}) {
	const [active, setActive] = useState<number | null>(null);
	const max = Math.max(1, ...buckets.map((b) => b.segments.reduce((a, s) => a + s.value, 0)));
	const last = buckets.length - 1;
	const current = active === null ? null : Math.min(active, last);
	const move = (to: number) => {
		setActive(Math.max(0, Math.min(last, to)));
	};
	const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
		const at = current ?? last;
		switch (e.key) {
			case "ArrowLeft": {
				e.preventDefault();
				move(current === null ? last : at - 1);
				return;
			}
			case "ArrowRight": {
				e.preventDefault();
				move(current === null ? 0 : at + 1);
				return;
			}
			case "Home": {
				e.preventDefault();
				move(0);
				return;
			}
			case "End": {
				e.preventDefault();
				move(last);
				return;
			}
			case "Enter": {
				e.preventDefault();
				if (current !== null) onPick(current);
				return;
			}
			default:
				return;
		}
	};
	const bucketAt = (e: React.MouseEvent<HTMLDivElement>) => {
		const rect = e.currentTarget.getBoundingClientRect();
		return Math.floor(((e.clientX - rect.left) / rect.width) * buckets.length);
	};
	return (
		<div
			role="slider"
			tabIndex={0}
			aria-label={label}
			aria-valuemin={0}
			aria-valuemax={last}
			aria-valuenow={current ?? last}
			aria-valuetext={buckets[current ?? last]?.label ?? ""}
			title={current === null ? undefined : buckets[current]?.label}
			onKeyDown={onKeyDown}
			onBlur={() => {
				setActive(null);
			}}
			onMouseMove={(e) => {
				move(bucketAt(e));
			}}
			onMouseLeave={() => {
				setActive(null);
			}}
			onClick={(e) => {
				onPick(Math.max(0, Math.min(last, bucketAt(e))));
			}}
			className="flex h-[72px] cursor-pointer items-end gap-0.5 rounded-sm focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-4 focus-visible:ring-offset-panel focus-visible:outline-none"
		>
			{buckets.map((b, i) => (
				<div
					key={String(i)}
					className={cn(
						"flex h-full min-w-0.5 flex-1 flex-col-reverse overflow-hidden rounded-t-[2px]",
						current === i && "bg-panel2 brightness-90",
					)}
				>
					{b.segments.map((s, j) => (
						<span
							key={String(j)}
							className="block w-full shrink-0"
							style={{ height: `${String((s.value / max) * 100)}%`, background: s.color }}
						/>
					))}
				</div>
			))}
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
				disabled ? "cursor-not-allowed text-fg4" : "cursor-pointer hover:border-fg4 hover:bg-panel",
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
				aria-label="Newer"
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
				aria-label="Older"
				disabled={onNext === null}
				onClick={onNext ?? undefined}
				className="text-fg3 hover:text-foreground disabled:opacity-35"
			>
				<ChevronDownIcon className="size-[15px]" />
			</Button>
			<Button
				variant="ghost"
				size="icon-sm"
				title="Close"
				aria-label="Close"
				onClick={onClose}
				className="text-fg3 hover:text-foreground"
			>
				<XIcon className="size-3.5" />
			</Button>
		</>
	);
}

export const PAGE_SIZES = [25, 50, 100];

export function readPaging(params: URLSearchParams, defaultSize: number): Paging {
	const page = Number(params.get("page"));
	const size = Number(params.get("size"));
	return {
		page: Number.isSafeInteger(page) && page > 0 ? page - 1 : 0,
		size: PAGE_SIZES.includes(size) ? size : defaultSize,
	};
}

export function DetailPanel({ label, header, children }: { label: string; header: ReactNode; children: ReactNode }) {
	return (
		<aside
			aria-label={label}
			className="sticky top-4 flex max-h-[calc(100dvh-32px)] min-w-0 flex-col overflow-hidden rounded-overlay border border-line bg-background"
		>
			<div className="flex items-start gap-1 border-b border-line py-3 pr-2.5 pl-4">{header}</div>
			{children}
		</aside>
	);
}
