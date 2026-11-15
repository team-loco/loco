import type { Zoom } from "../flame";

export function boundedZoom(a: number, width: number): Zoom {
	const w = Math.max(0.002, Math.min(1, width));
	const left = Math.max(0, Math.min(1 - w, a));
	return { a: left, b: left + w };
}

export function zoomAround(zoom: Zoom, anchor: number, delta: number): Zoom {
	const width = zoom.b - zoom.a;
	const next = Math.min(1, Math.max(0.002, width * Math.exp(delta * 0.002)));
	return boundedZoom(zoom.a + anchor * (width - next), next);
}

export function offsetTicks(duration: bigint, zoom: Zoom): { left: number; nanos: bigint }[] {
	const total = Number(duration);
	const width = zoom.b - zoom.a;
	if (total <= 0) return [];
	const target = (total * width) / 5;
	const magnitude = 10 ** Math.floor(Math.log10(target));
	const step = ([1, 2, 5, 10].find((n) => n * magnitude >= target) ?? 10) * magnitude;
	const ticks = [];
	for (let value = Math.ceil((total * zoom.a) / step) * step; value <= total * zoom.b; value += step) {
		ticks.push({ left: (value / total - zoom.a) / width, nanos: BigInt(Math.round(value)) });
	}
	return ticks;
}
