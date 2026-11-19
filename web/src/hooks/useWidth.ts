import { useCallback, useState } from "react";

/** The content width of the element given the returned ref, kept current as it resizes. */
export function useWidth(): [number, (el: HTMLElement | null) => (() => void) | undefined] {
	const [width, setWidth] = useState(0);
	const measure = useCallback((el: HTMLElement | null) => {
		if (el === null) return undefined;
		const observer = new ResizeObserver(([entry]) => {
			if (entry !== undefined) setWidth(entry.contentRect.width);
		});
		observer.observe(el);
		return () => {
			observer.disconnect();
		};
	}, []);
	return [width, measure];
}
