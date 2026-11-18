import type { ChartMarker } from "./MetricChart";

// Deploys closer than this share of the range share one line and label.
const MERGE_SHARE = 0.04;

export interface MarkerGroup {
	t: number;
	label: string;
}

/** Merges deploys that would draw on top of each other into one line, labelled by the first. */
export function markerGroups(markers: ChartMarker[], from: number, to: number): MarkerGroup[] {
	const gap = (to - from) * MERGE_SHARE;
	const groups: { t: number; labels: string[] }[] = [];
	for (const m of markers.toSorted((a, b) => a.t - b.t)) {
		const last = groups.at(-1);
		if (last !== undefined && m.t - last.t < gap) last.labels.push(m.label);
		else groups.push({ t: m.t, labels: [m.label] });
	}
	return groups.map((g) => ({
		t: g.t,
		label: g.labels.length > 1 ? `${g.labels[0] ?? ""} +${String(g.labels.length - 1)}` : (g.labels[0] ?? ""),
	}));
}
