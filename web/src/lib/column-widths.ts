export function validColumnWidths(value: unknown, count: number): value is number[] {
	return Array.isArray(value)
		&& value.length === count
		&& value.every((width: unknown) => typeof width === "number" && Number.isFinite(width) && width > 0)
		&& Number.isFinite(value.reduce((sum: number, width: number) => sum + width, 0));
}

export function fitColumnWidths(
	weights: readonly number[],
	minimums: readonly number[],
	available: number,
): number[] {
	const widths = weights.map(() => 0);
	let remaining = Math.max(available, minimums.reduce((sum, width) => sum + width, 0));
	let pending = weights.map((_, index) => index);
	while (pending.length > 0) {
		const sum = pending.reduce((total, index) => total + (weights[index] ?? 0), 0);
		const small = pending.filter(
			(index) => ((weights[index] ?? 0) / sum) * remaining < (minimums[index] ?? 0),
		);
		if (small.length === 0) {
			for (const index of pending) widths[index] = ((weights[index] ?? 0) / sum) * remaining;
			break;
		}
		for (const index of small) {
			widths[index] = minimums[index] ?? 0;
			remaining -= widths[index];
		}
		pending = pending.filter((index) => !small.includes(index));
	}
	return widths;
}

export function resizeColumnPair(
	widths: readonly number[],
	minimums: readonly number[],
	index: number,
	requested: number,
): number[] {
	const next = [...widths];
	const pair = (widths[index] ?? 0) + (widths[index + 1] ?? 0);
	const width = Math.min(
		pair - (minimums[index + 1] ?? 0),
		Math.max(minimums[index] ?? 0, requested),
	);
	next[index] = width;
	next[index + 1] = pair - width;
	return next;
}
