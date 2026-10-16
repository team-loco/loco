import { useRef } from "react";
import "./ColumnResizeHandle.css";

const KEY_STEP = 8;
const COARSE_STEP = 32;

function reportWidth(target: HTMLButtonElement, width: number) {
	target.setAttribute("aria-valuenow", String(Math.round(width)));
	target.setAttribute("aria-valuetext", `${String(Math.round(width))} pixels`);
}

export function ColumnResizeHandle({
	label,
	value,
	min,
	max,
	onResize,
}: {
	label: string;
	value: number;
	min: number;
	max: number;
	onResize: (value: number, commit: boolean) => void;
}) {
	const dragRef = useRef<{
		pointerId: number;
		x: number;
		value: number;
	} | null>(null);
	const clamp = (width: number) => Math.min(max, Math.max(min, width));
	const cancel = (target: HTMLButtonElement) => {
		const drag = dragRef.current;
		if (drag === null) return;
		dragRef.current = null;
		reportWidth(target, drag.value);
		onResize(drag.value, false);
		if (target.hasPointerCapture(drag.pointerId)) {
			target.releasePointerCapture(drag.pointerId);
		}
	};
	return (
		<button
			type="button"
			role="separator"
			aria-label={label}
			aria-orientation="vertical"
			aria-valuenow={Math.round(value)}
			aria-valuemin={Math.round(min)}
			aria-valuemax={Math.round(max)}
			aria-valuetext={`${String(Math.round(value))} pixels`}
			className="column-resize-handle"
			title={`${label}. Drag or use arrow keys; Shift changes faster. Home and End reach the limits.`}
			onClick={(event) => {
				event.stopPropagation();
			}}
			onPointerDown={(event) => {
				if (event.button !== 0 || !event.isPrimary || dragRef.current !== null) return;
				event.preventDefault();
				event.stopPropagation();
				event.currentTarget.focus();
				event.currentTarget.setPointerCapture(event.pointerId);
				dragRef.current = { pointerId: event.pointerId, x: event.clientX, value };
			}}
			onPointerMove={(event) => {
				const drag = dragRef.current;
				if (drag?.pointerId !== event.pointerId) return;
				const width = clamp(drag.value + event.clientX - drag.x);
				reportWidth(event.currentTarget, width);
				onResize(width, false);
			}}
			onPointerUp={(event) => {
				const drag = dragRef.current;
				if (drag?.pointerId !== event.pointerId) return;
				const width = clamp(drag.value + event.clientX - drag.x);
				dragRef.current = null;
				event.currentTarget.releasePointerCapture(event.pointerId);
				onResize(width, true);
			}}
			onPointerCancel={(event) => {
				if (dragRef.current?.pointerId === event.pointerId) cancel(event.currentTarget);
			}}
			onLostPointerCapture={(event) => {
				if (dragRef.current?.pointerId === event.pointerId) cancel(event.currentTarget);
			}}
			onKeyDown={(event) => {
				if (event.key === "Escape" && dragRef.current !== null) {
					event.preventDefault();
					event.stopPropagation();
					cancel(event.currentTarget);
					return;
				}
				if (dragRef.current !== null) return;
				const step = event.shiftKey ? COARSE_STEP : KEY_STEP;
				let width: number;
				switch (event.key) {
					case "ArrowLeft":
						width = value - step;
						break;
					case "ArrowRight":
						width = value + step;
						break;
					case "Home":
						width = min;
						break;
					case "End":
						width = max;
						break;
					default:
						return;
				}
				event.preventDefault();
				event.stopPropagation();
				onResize(clamp(width), true);
			}}
		/>
	);
}
