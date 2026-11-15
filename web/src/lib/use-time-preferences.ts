import { useStoredValue } from "./storage";

export function useTimePreferences() {
	const [zone, setZone] = useStoredValue("loco-tz", "local");
	const [clock, setClock] = useStoredValue("loco-clock", "24");
	return { zone: zone === "utc" ? "utc" : "local", clock: clock === "12" ? "12" : "24", setZone, setClock } as const;
}
