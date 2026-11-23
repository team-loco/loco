const NICE_STEPS = [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10];
const HEADROOM = 1.06;
const LIMIT_HEADROOM = 1.12;
const MARKER_GAP_PX = 4;
const MARKER_PAD_PX = 5;
const MIN_LABEL_CHARS = 3;

/** The top of the y axis: a round number above the peak and the limit, with room for the limit's label. */
export function chartMax(peak: number, limit: number | undefined): number {
	const raw = Math.max(1e-9, peak, limit === undefined ? 0 : limit * LIMIT_HEADROOM);
	const want = raw * HEADROOM;
	const magnitude = 10 ** Math.floor(Math.log10(want));
	return (NICE_STEPS.find((step) => step * magnitude >= want) ?? 10) * magnitude;
}

export interface PlacedMarker {
	x: number;
	label: string;
	/** The label sits left of its line. */
	flip: boolean;
	/** Where the label's text starts. */
	textX: number;
}

/** Shortens a label to fit `room` pixels, or returns undefined when not even a few characters fit. */
export function fitLabel(label: string, room: number, charPx: number): string | undefined {
	const chars = Math.floor(room / charPx);
	if (chars >= label.length) return label;
	if (chars < MIN_LABEL_CHARS) return undefined;
	return `${label.slice(0, chars - 1)}…`;
}

/**
 * Lays marker labels along the top of the plot, left to right. A label runs right from its line, or left of it
 * past three quarters of the width, and shrinks so it never crosses the next line or the label before it.
 */
export function placeMarkers(
	markers: readonly { x: number; label: string }[],
	left: number,
	right: number,
	charPx: number,
): PlacedMarker[] {
	const sorted = markers.toSorted((a, b) => a.x - b.x);
	const placed: PlacedMarker[] = [];
	let taken = left;
	sorted.forEach((m, i) => {
		const next = sorted[i + 1]?.x ?? right;
		const flip = (m.x - left) / Math.max(1, right - left) > 0.75;
		const start = flip ? Math.max(taken, (sorted[i - 1]?.x ?? left) + MARKER_GAP_PX) : m.x + MARKER_PAD_PX;
		const end = flip ? m.x - MARKER_PAD_PX : Math.min(right, next - MARKER_GAP_PX);
		const label = fitLabel(m.label, end - start, charPx);
		if (label === undefined) return;
		const width = label.length * charPx;
		const textX = flip ? end - width : start;
		placed.push({ x: m.x, label, flip, textX });
		taken = Math.max(taken, textX + width);
	});
	return placed;
}
