import { useId } from "react";

import { ThemePreview } from "@/components/design/ThemePreview";
import { colorThemes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { SettingsCard, SettingsSection } from "./parts";

export function AppearanceSection() {
	const { color, setColor } = useTheme();
	const groupId = useId();
	const labelId = useId();

	return (
		<SettingsSection title="Appearance" meta="saved in this browser">
			<SettingsCard className="flex flex-col gap-2.5 px-5 py-[18px]">
				<span id={labelId} className="text-[12.5px] text-fg3">
					Colour theme
				</span>
				<div
					role="radiogroup"
					aria-labelledby={labelId}
					className="grid grid-cols-[repeat(auto-fill,minmax(140px,1fr))] gap-3"
				>
					{colorThemes.map((theme) => (
						<label
							key={theme.id}
							htmlFor={`${groupId}-${theme.id}`}
							className="flex flex-col gap-2 rounded-card bg-raise px-1.5 pt-1.5 pb-2 ring-1 ring-line2 hover:ring-fg4 has-checked:ring-2 has-checked:ring-foreground has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-foreground"
						>
							<input
								id={`${groupId}-${theme.id}`}
								type="radio"
								name={groupId}
								value={theme.id}
								checked={color === theme.id}
								onChange={() => {
									setColor(theme.id);
								}}
								className="sr-only"
							/>
							<ThemePreview color={theme.id} />
							<span className="px-1 text-[13px]">{theme.name}</span>
						</label>
					))}
				</div>
			</SettingsCard>
		</SettingsSection>
	);
}
