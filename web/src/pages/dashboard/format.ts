import { formatHourMinute, formatMonthDay } from "@/lib/time";

export function startedLabel(ms: number): string {
	return ms === 0 ? "—" : `${formatMonthDay(ms)}, ${formatHourMinute(ms)}`;
}
