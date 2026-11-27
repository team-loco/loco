import { useId } from "react";

import { Switch } from "@/components/design/Switch";
import { useLogPreferences } from "@/lib/use-log-preferences";

import { SettingsCard, SettingsSection } from "./parts";

export function LogsSection() {
	const { parsedJson, setParsedJson } = useLogPreferences();
	const labelId = useId();
	const descriptionId = useId();
	return (
		<SettingsSection title="Logs" meta="saved in this browser">
			<SettingsCard className="flex items-center justify-between gap-4 px-5 py-[18px]">
				<span className="flex min-w-0 flex-col gap-0.5">
					<span id={labelId} className="text-[13px] font-medium">
						Parsed JSON
					</span>
					<span id={descriptionId} className="text-[12.5px] text-fg3">
						Show JSON objects and arrays inside log attributes as structured values.
					</span>
				</span>
				<Switch
					checked={parsedJson}
					onCheckedChange={setParsedJson}
					aria-labelledby={labelId}
					aria-describedby={descriptionId}
				/>
			</SettingsCard>
		</SettingsSection>
	);
}
