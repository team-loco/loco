import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useTimePreferences } from "@/lib/use-time-preferences";

const localZone = new Intl.DateTimeFormat().resolvedOptions().timeZone;

export function TimeZoneControl({ onChange }: { onChange?: (zone: "local" | "utc") => void }) {
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
				Local · {localZone}
			</ToggleGroupItem>
			<ToggleGroupItem value="utc">UTC</ToggleGroupItem>
		</ToggleGroup>
	);
}
