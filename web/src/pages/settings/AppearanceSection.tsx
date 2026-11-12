import { useId } from "react";

import { RadioGroup, RadioCard } from "@/components/design/RadioGroup";
import { ModeIcon, ThemePreview } from "@/components/design/ThemePreview";
import { colorThemes, modes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { SettingsCard, SettingsSection } from "./parts";

const cardGrid = "grid-cols-[repeat(auto-fill,minmax(140px,1fr))]";

export function AppearanceSection() {
	const { mode, setMode, color, setColor } = useTheme();
	const modeLabelId = useId();
	const colorLabelId = useId();

	return (
		<SettingsSection title="Appearance" meta="saved in this browser">
			<SettingsCard className="flex flex-col gap-5 px-5 py-[18px]">
				<div className="flex flex-col gap-2.5">
					<span id={modeLabelId} className="text-[12.5px] text-fg3">
						Mode
					</span>
					<RadioGroup
						aria-labelledby={modeLabelId}
						value={mode}
						onValueChange={(value) => {
							const next = modes.find((item) => item.id === value);
							if (next !== undefined) setMode(next.id);
						}}
						className={cardGrid}
					>
						{modes.map((item) => (
							<RadioCard key={item.id} value={item.id} aria-label={item.name}>
								<ThemePreview color={color} mode={item.id} />
								<span className="flex items-center gap-[7px] px-1 text-[13px] [&_svg]:size-3.5 [&_svg]:text-fg3">
									<ModeIcon mode={item.id} />
									{item.name}
								</span>
							</RadioCard>
						))}
					</RadioGroup>
				</div>
				<div className="flex flex-col gap-2.5">
					<span id={colorLabelId} className="text-[12.5px] text-fg3">
						Color theme
					</span>
					<RadioGroup
						aria-labelledby={colorLabelId}
						value={color}
						onValueChange={(value) => {
							const theme = colorThemes.find((item) => item.id === value);
							if (theme !== undefined) setColor(theme.id);
						}}
						className={cardGrid}
					>
						{colorThemes.map((theme) => (
							<RadioCard key={theme.id} value={theme.id} aria-label={theme.name}>
								<ThemePreview color={theme.id} />
								<span className="px-1 text-[13px]">{theme.name}</span>
							</RadioCard>
						))}
					</RadioGroup>
				</div>
			</SettingsCard>
		</SettingsSection>
	);
}
