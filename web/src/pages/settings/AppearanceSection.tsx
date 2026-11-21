import { useId } from "react";

import { Switch } from "@/components/design/Switch";
import { RadioGroup, RadioCard } from "@/components/design/RadioGroup";
import { ModeIcon, ThemePreview } from "@/components/design/ThemePreview";
import { useStoredValue } from "@/lib/storage";
import { colorThemes, modes, soundsKey } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { SettingsCard, SettingsSection } from "./parts";

const modeGrid = "grid-cols-[repeat(auto-fill,200px)]";
const colorGrid = "grid-cols-[repeat(auto-fill,minmax(140px,1fr))]";
const label = "text-[13px] font-medium";

export function AppearanceSection() {
	const { mode, setMode, color, setColor } = useTheme();
	const [sounds, setSounds] = useStoredValue(soundsKey, "on");
	const modeLabelId = useId();
	const colorLabelId = useId();
	const soundsLabelId = useId();

	return (
		<SettingsSection title="Appearance" meta="saved in this browser">
			<SettingsCard className="flex flex-col gap-5 px-5 py-[18px]">
				<div className="flex flex-col gap-2.5">
					<span id={modeLabelId} className={label}>
						Mode
					</span>
					<RadioGroup
						aria-labelledby={modeLabelId}
						value={mode}
						onValueChange={(value) => {
							const next = modes.find((item) => item.id === value);
							if (next !== undefined) setMode(next.id);
						}}
						className={modeGrid}
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
					<span id={colorLabelId} className={label}>
						Color theme
					</span>
					<RadioGroup
						aria-labelledby={colorLabelId}
						value={color}
						onValueChange={(value) => {
							const theme = colorThemes.find((item) => item.id === value);
							if (theme !== undefined) setColor(theme.id);
						}}
						className={colorGrid}
					>
						{colorThemes.map((theme) => (
							<RadioCard key={theme.id} value={theme.id} aria-label={theme.name}>
								<ThemePreview color={theme.id} />
								<span className="px-1 text-[13px]">{theme.name}</span>
							</RadioCard>
						))}
					</RadioGroup>
				</div>
				<div className="flex items-center justify-between gap-4">
					<span className="flex flex-col gap-0.5">
						<span id={soundsLabelId} className={label}>
							Sounds
						</span>
						<span className="text-[12.5px] text-fg3">Play a sound when the mode switches between light and dark.</span>
					</span>
					<Switch
						checked={sounds !== "off"}
						aria-labelledby={soundsLabelId}
						onCheckedChange={(checked: boolean) => {
							setSounds(checked ? "on" : "off");
						}}
					/>
				</div>
			</SettingsCard>
		</SettingsSection>
	);
}
