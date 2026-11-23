import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { timeZoneLabel } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

const localZone = new Intl.DateTimeFormat().resolvedOptions().timeZone;
const localZoneShort = timeZoneLabel(Date.now(), { zone: "local", clock: "24" });

/** `short` names the local zone by its abbreviation, like PST, in place of its IANA name. */
export function TimeZoneControl({
	onChange,
	short = false,
}: {
	onChange?: (zone: "local" | "utc") => void;
	short?: boolean;
}) {
	const { zone, setZone } = useTimePreferences();
	return (
		<ToggleGroup
			variant="segmented"
			aria-label="Time zone"
			value={[zone]}
			onValueChange={(values) => {
				const next = values[0];
				if (next !== "local" && next !== "utc") return;
				setZone(next);
				onChange?.(next);
			}}
			className="max-w-full flex-wrap self-start"
		>
			<ToggleGroupItem value="local" className="h-auto min-h-[26px] whitespace-normal">
				Local ({short ? localZoneShort : localZone})
			</ToggleGroupItem>
			<ToggleGroupItem value="utc">UTC</ToggleGroupItem>
		</ToggleGroup>
	);
}
