import { useRef } from "react";

export function ColumnResizeHandle({
	label,
	width,
	min,
	max,
	onWidth,
}: {
	label: string;
	width: number;
	min: number;
	max: number;
	onWidth: (value: number) => void;
}) {
	const dragRef = useRef<{ x: number; width: number } | null>(null);
	const resize = (value: number) => {
		onWidth(Math.min(max, Math.max(min, Math.round(value))));
	};
	return (
		<div
			role="separator"
			aria-orientation="vertical"
			aria-label={`Resize ${label}`}
			aria-valuenow={width}
			aria-valuemin={min}
			aria-valuemax={max}
			tabIndex={0}
			onKeyDown={(event) => {
				if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
					event.preventDefault();
					resize(width + (event.key === "ArrowLeft" ? -16 : 16));
				}
				if (event.key === "Escape" && dragRef.current !== null) {
					resize(dragRef.current.width);
					dragRef.current = null;
				}
			}}
			onPointerDown={(event) => {
				event.currentTarget.setPointerCapture(event.pointerId);
				dragRef.current = { x: event.clientX, width };
			}}
			onPointerMove={(event) => {
				if (dragRef.current !== null) resize(dragRef.current.width + event.clientX - dragRef.current.x);
			}}
			onPointerUp={() => {
				dragRef.current = null;
			}}
			onPointerCancel={() => {
				if (dragRef.current !== null) resize(dragRef.current.width);
				dragRef.current = null;
			}}
			onLostPointerCapture={() => {
				dragRef.current = null;
			}}
			className="absolute top-2 right-0 bottom-2 w-2 cursor-col-resize touch-none rounded-sm outline-none after:absolute after:inset-y-0 after:right-[3px] after:w-px after:bg-line2 hover:after:bg-fg4 focus-visible:after:bg-foreground"
		/>
	);
}
