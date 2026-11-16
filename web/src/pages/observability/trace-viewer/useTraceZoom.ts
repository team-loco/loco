import { useEffect, useRef, useState } from "react";
import { FULL_ZOOM, type Zoom } from "../flame";

export type SetTraceZoom = (next: Zoom | ((zoom: Zoom) => Zoom), immediate?: boolean) => void;

export function useTraceZoom() {
	const [zoom, setZoom] = useState(FULL_ZOOM);
	const currentRef = useRef(FULL_ZOOM);
	const pendingRef = useRef(FULL_ZOOM);
	const frameRef = useRef<number | null>(null);
	useEffect(
		() => () => {
			if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
		},
		[],
	);
	const change: SetTraceZoom = (next, immediate = false) => {
		const target = typeof next === "function" ? next(pendingRef.current) : next;
		pendingRef.current = target;
		if (immediate) {
			if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
			frameRef.current = requestAnimationFrame(() => {
				frameRef.current = null;
				currentRef.current = pendingRef.current;
				setZoom(pendingRef.current);
			});
			return;
		}
		if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
		const from = currentRef.current;
		const start = performance.now();
		const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
		const tick = (now: number) => {
			const t = reduced ? 1 : Math.min(1, (now - start) / 220);
			const ease = 1 - (1 - t) ** 3;
			currentRef.current =
				t === 1
					? target
					: {
							a: from.a + (target.a - from.a) * ease,
							b: from.b + (target.b - from.b) * ease,
						};
			setZoom(currentRef.current);
			frameRef.current = t === 1 ? null : requestAnimationFrame(tick);
		};
		frameRef.current = requestAnimationFrame(tick);
	};
	return { zoom, change };
}
