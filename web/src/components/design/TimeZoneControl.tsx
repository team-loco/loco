import { useState } from "react";

import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { timeZoneLabel } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

const localZone = new Intl.DateTimeFormat().resolvedOptions().timeZone;

/** `short` names the local zone by its abbreviation, like PST, in place of its IANA name. */
export function TimeZoneControl({
	onChange,
	short = false,
}: {
	onChange?: (zone: "local" | "utc") => void;
	short?: boolean;
}) {
	const pref = useTimePreferences();
	const [now] = useState(Date.now);
	const { zone, setZone } = pref;
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
				Local · {short ? timeZoneLabel(now, { ...pref, zone: "local" }) : localZone}
			</ToggleGroupItem>
			<ToggleGroupItem value="utc">UTC</ToggleGroupItem>
		</ToggleGroup>
	);
}
