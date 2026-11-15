import { formatHourMinute, formatMonthDay, type TimePreferences } from "@/lib/time";

export function startedLabel(ms: number, pref: TimePreferences): string {
	return ms === 0 ? "—" : `${formatMonthDay(ms, pref)}, ${formatHourMinute(ms, pref)}`;
}
