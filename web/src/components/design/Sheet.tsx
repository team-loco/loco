import { useRef } from "react";

import { keepOpenOnToastPress } from "@/components/design/Sonner";
import {
	Sheet as SheetBase,
	SheetContent as SheetContentBase,
	SheetDescription as SheetDescriptionBase,
	SheetTitle as SheetTitleBase,
} from "@/components/ui/sheet";
import { layoutNumber, useLayout } from "@/lib/layout";
import { cn } from "@/lib/utils";

const sheetWidths = {
	default: "data-[side=right]:w-[min(440px,calc(100vw-16px))]",
	wide: "data-[side=right]:w-[min(520px,calc(100vw-16px))]",
	full: "data-[side=right]:w-[min(1120px,calc(100vw-16px))]",
} as const;

const MIN_WIDTH = 380;
const KEY_STEP = 16;
const EDGE_GAP = 8;
const parseDrawerWidth = layoutNumber(0, MIN_WIDTH, Number.POSITIVE_INFINITY);

function clampWidth(width: number): number {
	return Math.round(Math.min(window.innerWidth - 2 * EDGE_GAP, Math.max(MIN_WIDTH, width)));
}

function SheetResizeHandle({ width, onWidth }: { width: number; onWidth: (width: number | null) => void }) {
	const dragRef = useRef(false);
	const current = (el: HTMLElement) => el.parentElement?.getBoundingClientRect().width ?? MIN_WIDTH;
	return (
		<div
			role="separator"
			aria-orientation="vertical"
			aria-label="Resize drawer"
			aria-valuenow={width}
			aria-valuemin={MIN_WIDTH}
			title="Drag to resize, double-click to reset"
			tabIndex={0}
			onKeyDown={(event) => {
				const dir = event.key === "ArrowLeft" ? 1 : event.key === "ArrowRight" ? -1 : 0;
				if (dir === 0) return;
				event.preventDefault();
				onWidth(clampWidth(current(event.currentTarget) + dir * KEY_STEP));
			}}
			onDoubleClick={() => {
				onWidth(null);
			}}
			onPointerDown={(event) => {
				event.currentTarget.setPointerCapture(event.pointerId);
				dragRef.current = true;
			}}
			onPointerMove={(event) => {
				if (dragRef.current) onWidth(clampWidth(window.innerWidth - EDGE_GAP - event.clientX));
			}}
			onPointerUp={() => {
				dragRef.current = false;
			}}
			onLostPointerCapture={() => {
				dragRef.current = false;
			}}
			className="group/handle absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize touch-none outline-none"
		>
			<span className="absolute top-1/2 left-[3px] h-9 w-0.5 -translate-y-1/2 rounded-sm bg-fg4 opacity-0 transition-opacity duration-120 group-hover/handle:opacity-100 group-focus-visible/handle:bg-foreground group-focus-visible/handle:opacity-100" />
		</div>
	);
}

function SheetContent({
	className,
	side = "right",
	size = "default",
	resizeKey,
	children,
	...props
}: React.ComponentProps<typeof SheetContentBase> & {
	size?: keyof typeof sheetWidths;
	/** Name in the viewer's layout; when set, the drawer's inner edge resizes it and the width is remembered. */
	resizeKey?: string;
}) {
	const [width, setWidth] = useLayout(resizeKey === undefined ? null : `drawer.${resizeKey}`, parseDrawerWidth);
	const resizable = resizeKey !== undefined && side === "right";
	return (
		<SheetContentBase
			side={side}
			className={cn(
				"gap-0 border border-line bg-background text-base shadow-overlay duration-[320ms] ease-[cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:transition-none dark:border-line2 data-[side=right]:inset-y-2 data-[side=right]:right-2 data-[side=right]:h-auto data-[side=right]:max-w-none data-[side=right]:rounded-overlay data-[side=right]:sm:max-w-none data-[side=right]:data-ending-style:translate-x-[calc(100%+16px)] data-[side=right]:data-starting-style:translate-x-[calc(100%+16px)] [&>[data-slot=sheet-close]]:top-[18px] [&>[data-slot=sheet-close]]:right-4 [&>[data-slot=sheet-close]]:text-fg3",
				sheetWidths[size],
				className,
			)}
			{...props}
			{...(resizable && width > 0 ? { style: { width: `min(${String(width)}px, calc(100vw - 16px))` } } : {})}
		>
			{resizable && <SheetResizeHandle width={width} onWidth={setWidth} />}
			{children}
		</SheetContentBase>
	);
}

function Sheet({ onOpenChange, ...props }: React.ComponentProps<typeof SheetBase>) {
	return <SheetBase onOpenChange={keepOpenOnToastPress(onOpenChange)} {...props} />;
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-header"
			className={cn(
				"relative flex min-h-16 shrink-0 items-center gap-3 border-b border-line py-3 pr-14 pl-6",
				className,
			)}
			{...props}
		/>
	);
}

function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-body"
			className={cn("flex min-h-0 flex-1 flex-col gap-[18px] overflow-y-auto px-6 py-5", className)}
			{...props}
		/>
	);
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-footer"
			className={cn("flex shrink-0 justify-end gap-2 border-t border-line px-6 py-3.5", className)}
			{...props}
		/>
	);
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof SheetTitleBase>) {
	return <SheetTitleBase className={cn("text-[20px] font-normal tracking-[-0.02em]", className)} {...props} />;
}

function SheetDescription({ className, ...props }: React.ComponentProps<typeof SheetDescriptionBase>) {
	return <SheetDescriptionBase className={cn("text-sm text-fg3", className)} {...props} />;
}

export { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle };
