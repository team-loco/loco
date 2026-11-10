const startedFormat = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "numeric",
	hour: "2-digit",
	minute: "2-digit",
	hour12: false,
});

export function startedLabel(ms: number): string {
	return ms === 0 ? "—" : startedFormat.format(ms);
}
