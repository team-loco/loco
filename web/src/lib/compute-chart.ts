const PERCENT = 100;
const AXIS_INTERVALS = 4;

export function formatPercent(value: number): string {
	return `${value < 10 && value !== 0 ? value.toFixed(1) : Math.round(value).toString()}%`;
}

export function percentAxis(peak: number) {
	const maximum = Math.max(PERCENT, Math.ceil(peak / PERCENT) * PERCENT);
	return { maximum, ticks: Array.from({ length: AXIS_INTERVALS + 1 }, (_, i) => (maximum * i) / AXIS_INTERVALS) };
}

export function timeAxis(from: number, to: number) {
	return Array.from({ length: AXIS_INTERVALS + 1 }, (_, i) => from + ((to - from) * i) / AXIS_INTERVALS);
}
