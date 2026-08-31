import type { Timestamp } from "@bufbuild/protobuf/wkt";

/** Milliseconds elapsed since a protobuf timestamp. */
export function millisSince(timestamp: Timestamp): number {
	return Date.now() - Number(timestamp.seconds) * 1000;
}

/**
 * Coarse "how long ago", to the hour: "just now", "1h ago", "5d ago".
 * Used on the dashboard lists, where minute precision is noise.
 */
export function hoursAgo(timestamp: Timestamp): string {
	const diff = millisSince(timestamp);
	const hours = Math.floor(diff / 3_600_000);
	const days = Math.floor(diff / 86_400_000);

	if (hours === 0) return "just now";
	if (hours === 1) return "1h ago";
	if (hours < 24) return `${hours.toString()}h ago`;
	if (days === 1) return "1d ago";
	return `${days.toString()}d ago`;
}

/**
 * Finer-grained "how long ago", down to the minute, with a capitalised
 * leading word: "Just now", "12m ago", "Yesterday". Used on detail views.
 */
export function relativeTime(timestamp: Timestamp | undefined): string {
	if (!timestamp) return "—";
	const diff = millisSince(timestamp);
	const mins = Math.floor(diff / 60_000);
	const hrs = Math.floor(diff / 3_600_000);
	const days = Math.floor(diff / 86_400_000);

	if (mins < 1) return "Just now";
	if (mins < 60) return `${mins.toString()}m ago`;
	if (hrs < 24) return `${hrs.toString()}h ago`;
	if (days === 1) return "Yesterday";
	return `${days.toString()}d ago`;
}
