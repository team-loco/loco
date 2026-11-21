const PERCENT = 100;
const AXIS_INTERVALS = 4;
const LIMIT_HEADROOM = 1.1;
const MIN_PERCENT = 0.1;

export function formatPercent(value: number): string {
	if (value > 0 && value < MIN_PERCENT) return `<${String(MIN_PERCENT)}%`;
	return `${value < 10 && value !== 0 ? value.toFixed(1) : Math.round(value).toString()}%`;
}

export function percentAxis(peak: number) {
	const maximum = Math.max(PERCENT, Math.ceil(peak / PERCENT) * PERCENT);
	return {
		maximum,
		ticks: Array.from({ length: AXIS_INTERVALS + 1 }, (_, i) => (maximum * i) / AXIS_INTERVALS),
	};
}

const NICE_STEPS = [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10];
const HEADROOM = 1.06;

/** A percent axis with room above the limit or the peak, labelled at its quarters. */
export function quarterAxis(peak: number) {
	const want = Math.max(peak, PERCENT) * HEADROOM;
	const magnitude = 10 ** Math.floor(Math.log10(want));
	const maximum = (NICE_STEPS.find((step) => step * magnitude >= want) ?? 10) * magnitude;
	return {
		maximum,
		ticks: Array.from({ length: AXIS_INTERVALS - 1 }, (_, i) => (maximum * (i + 1)) / AXIS_INTERVALS),
	};
}

// The axis stops just above the limit, or above the peak when usage bursts past it.
export function limitAxis(limit: number, peak: number) {
	return {
		maximum: Math.max(limit, peak) * LIMIT_HEADROOM,
		ticks: [0, limit / 2, limit],
	};
}
