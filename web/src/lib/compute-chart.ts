const PERCENT = 100;
const AXIS_INTERVALS = 4;
const LIMIT_HEADROOM = 1.1;

export function formatPercent(value: number): string {
	return `${value < 10 && value !== 0 ? value.toFixed(1) : Math.round(value).toString()}%`;
}

export function percentAxis(peak: number) {
	const maximum = Math.max(PERCENT, Math.ceil(peak / PERCENT) * PERCENT);
	return { maximum, ticks: Array.from({ length: AXIS_INTERVALS + 1 }, (_, i) => (maximum * i) / AXIS_INTERVALS) };
}

// The axis stops just above the limit, or above the peak when usage bursts past it.
export function limitAxis(limit: number, peak: number) {
	return { maximum: Math.max(limit, peak) * LIMIT_HEADROOM, ticks: [0, limit / 2, limit] };
}
