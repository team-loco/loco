import { useState } from "react";
import { TimeZoneControl } from "@/components/design/TimeZoneControl";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { formatFullTime } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { SettingsCard, SettingsSection } from "./parts";

export function DateTimeSection() {
	const pref = useTimePreferences();
	const { clock, setClock } = pref;
	const [now, setNow] = useState(Date.now);
	return (
		<SettingsSection title="Date and time" meta="saved in this browser">
			<SettingsCard className="flex flex-col gap-4 px-5 py-[18px]">
				<div className="flex flex-col gap-2">
					<span className="text-[12.5px] text-fg3">Time zone</span>
					<TimeZoneControl
						onChange={() => {
							setNow(Date.now());
						}}
					/>
				</div>
				<div className="flex flex-col gap-2">
					<span className="text-[12.5px] text-fg3">Clock</span>
					<ToggleGroup
						variant="segmented"
						aria-label="Clock"
						value={[clock]}
						onValueChange={(values) => {
							const next = values[0];
							if (next !== "24" && next !== "12") return;
							setClock(next);
							setNow(Date.now());
						}}
						className="self-start"
					>
						<ToggleGroupItem value="24">24-hour</ToggleGroupItem>
						<ToggleGroupItem value="12">12-hour</ToggleGroupItem>
					</ToggleGroup>
				</div>
				<div className="flex flex-col gap-2">
					<span className="text-[12.5px] text-fg3">Shown as</span>
					<time className="font-mono text-[13px]" dateTime={new Date(now).toISOString()}>
						{formatFullTime(now, true, pref)}
					</time>
				</div>
			</SettingsCard>
		</SettingsSection>
	);
}
