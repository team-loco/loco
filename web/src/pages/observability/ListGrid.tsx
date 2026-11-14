import { useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

import { clampWidth, type ColumnKey, type ColumnWidths } from "./columns";

const RESIZE_STEP = 16;

export const LIST_HEAD_CLASS = "grid h-[38px] items-center border-b border-line2 text-[12px] text-fg3";

/** The log and trace lists share one row look: zebra stripes, a hover fill and a raised selected row. */
export function listRowClass(index: number, selected: boolean): string {
	return cn(
		"grid min-h-[34px] w-full items-center rounded-lg text-left text-[12px] transition-[background] duration-900 ease-out hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
		index % 2 === 1 && "bg-[color-mix(in_oklab,var(--panel2)_45%,transparent)]",
		selected && "bg-background shadow-[inset_0_0_0_1px_var(--line2)] hover:bg-background",
	);
}

interface Drag {
	key: ColumnKey;
	startX: number;
	start: number;
	width: number;
}

/** Drag- and arrow-key-resizable columns; `live` holds the widths to lay out with, including a drag in progress. */
export function useResizableColumns(
	widths: ColumnWidths,
	onWidths: (next: ColumnWidths) => void,
	widthOf: (key: ColumnKey, widths: ColumnWidths) => number,
): { live: ColumnWidths; handle: (key: ColumnKey, label: string) => ReactNode } {
	const [drag, setDrag] = useState<Drag | null>(null);
	const live = drag === null ? widths : { ...widths, [drag.key]: drag.width };
	const resize = (key: ColumnKey, width: number) => {
		onWidths({ ...widths, [key]: clampWidth(width) });
	};
	const handle = (key: ColumnKey, label: string) => (
		<span
			role="separator"
			tabIndex={0}
			aria-orientation="vertical"
			aria-label={`Resize column ${label}`}
			aria-valuenow={widthOf(key, live)}
			className="absolute top-1.5 -right-1 bottom-1.5 z-10 w-2 cursor-col-resize touch-none after:absolute after:inset-y-0 after:left-[3px] after:w-px after:bg-line2 hover:after:bg-fg4 focus-visible:outline-none focus-visible:after:bg-ring"
			onPointerDown={(event) => {
				event.currentTarget.setPointerCapture(event.pointerId);
				const start = widthOf(key, widths);
				setDrag({ key, startX: event.clientX, start, width: start });
			}}
			onPointerMove={(event) => {
				if (drag?.key !== key) return;
				setDrag({ ...drag, width: clampWidth(drag.start + event.clientX - drag.startX) });
			}}
			onPointerUp={() => {
				if (drag?.key !== key) return;
				setDrag(null);
				resize(key, drag.width);
			}}
			onPointerCancel={() => {
				setDrag(null);
			}}
			onKeyDown={(event) => {
				const step = event.key === "ArrowRight" ? RESIZE_STEP : event.key === "ArrowLeft" ? -RESIZE_STEP : 0;
				if (step === 0) return;
				event.preventDefault();
				resize(key, widthOf(key, widths) + step);
			}}
		/>
	);
	return { live, handle };
}
