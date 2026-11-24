import { MonitorIcon, MoonIcon, SunIcon, type LucideIcon } from "lucide-react";

import { RadioCard, RadioGroup } from "@/components/design/RadioGroup";
import { modes, parseMode, type Mode } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import "./ModePicker.css";

const MODE_WIDTH = 30;
const MODE_PADDING = 2;

const MODE_ICONS: Record<Mode, LucideIcon> = { system: MonitorIcon, light: SunIcon, dark: MoonIcon };

export function ModePicker() {
	const { mode, setMode } = useTheme();
	return (
		<div className="umode flex h-10 items-center justify-between px-4 text-[14px]">
			<span>Mode</span>
			<RadioGroup
				aria-label="Mode"
				data-segmented
				aria-orientation="horizontal"
				value={mode}
				onValueChange={(value) => {
					setMode(parseMode(String(value)));
				}}
				onKeyDown={(event) => {
					if (event.key.startsWith("Arrow")) event.stopPropagation();
				}}
				style={{ padding: MODE_PADDING }}
				className="relative flex w-auto gap-0 rounded-control bg-panel2 shadow-[inset_0_1px_2px_color-mix(in_oklch,var(--fg)_8%,transparent)]"
			>
				<span
					aria-hidden
					className="mt-thumb"
					style={{
						top: MODE_PADDING,
						width: MODE_WIDTH,
						left: MODE_PADDING + modes.findIndex((item) => item.id === mode) * MODE_WIDTH,
					}}
				/>
				{modes.map((item) => {
					const Icon = MODE_ICONS[item.id];
					return (
						<RadioCard
							key={item.id}
							value={item.id}
							aria-label={item.name}
							title={item.name}
							style={{ width: MODE_WIDTH }}
							data-m={item.id}
							className="relative z-3 h-6 shrink-0 items-center justify-center rounded-control-inner bg-transparent p-0 text-fg3 ring-0 hover:ring-0 data-checked:bg-transparent data-checked:text-foreground data-checked:ring-0 dark:bg-transparent dark:data-checked:bg-transparent"
						>
							<Icon aria-hidden className="size-[15px]" />
						</RadioCard>
					);
				})}
			</RadioGroup>
		</div>
	);
}
