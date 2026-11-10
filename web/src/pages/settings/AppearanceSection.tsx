import { useId } from "react";

import { RadioGroup, RadioCard } from "@/components/design/RadioGroup";
import { ThemePreview } from "@/components/design/ThemePreview";
import { colorThemes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { SettingsCard, SettingsSection } from "./parts";

export function AppearanceSection() {
	const { color, setColor } = useTheme();
	const labelId = useId();

	return (
		<SettingsSection title="Appearance" meta="saved in this browser">
			<SettingsCard className="flex flex-col gap-2.5 px-5 py-[18px]">
				<span id={labelId} className="text-[12.5px] text-fg3">
					Colour theme
				</span>
				<RadioGroup
					aria-labelledby={labelId}
					value={color}
					onValueChange={(value) => {
						const theme = colorThemes.find((item) => item.id === value);
						if (theme !== undefined) setColor(theme.id);
					}}
					className="grid-cols-[repeat(auto-fill,minmax(140px,1fr))]"
				>
					{colorThemes.map((theme) => (
						<RadioCard key={theme.id} value={theme.id} aria-label={theme.name}>
							<ThemePreview color={theme.id} />
							<span className="px-1 text-[13px]">{theme.name}</span>
						</RadioCard>
					))}
				</RadioGroup>
			</SettingsCard>
		</SettingsSection>
	);
}
