import { formatHourMinute, formatMonthDay, timePreferences } from "@/lib/time";

export function startedLabel(ms: number, pref = timePreferences()): string {
	return ms === 0 ? "—" : `${formatMonthDay(ms, pref)}, ${formatHourMinute(ms, pref)}`;
}
