import { useEffect, useRef, useState } from "react";
import { FULL_ZOOM, type Zoom } from "../flame";

export type SetTraceZoom = (next: Zoom | ((zoom: Zoom) => Zoom), immediate?: boolean) => void;

export function useTraceZoom() {
	const [zoom, setZoom] = useState(FULL_ZOOM);
	const drawerRef = useRef<HTMLDivElement>(null);
	const pendingRef = useRef(FULL_ZOOM);
	const frameRef = useRef<number | null>(null);
	useEffect(
		() => () => {
			if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
		},
		[],
	);
	const change: SetTraceZoom = (next, immediate = false) => {
		pendingRef.current = typeof next === "function" ? next(pendingRef.current) : next;
		if (immediate) {
			drawerRef.current?.classList.remove("tv-anim");
			frameRef.current ??= requestAnimationFrame(() => {
				frameRef.current = null;
				setZoom(pendingRef.current);
			});
			return;
		}
		if (frameRef.current !== null) {
			cancelAnimationFrame(frameRef.current);
			frameRef.current = null;
		}
		drawerRef.current?.classList.add("tv-anim");
		setZoom(pendingRef.current);
	};
	return { zoom, change, drawerRef };
}
