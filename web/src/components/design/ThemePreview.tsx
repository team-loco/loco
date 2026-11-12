import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";

import type { ColorTheme, Mode } from "@/lib/theme";
import { cn } from "@/lib/utils";

function ThemeSwatch({ color, dark, className }: { color: ColorTheme; dark: boolean; className?: string }) {
	return (
		<span
			aria-hidden
			data-theme={color}
			className={cn(
				"size-4 shrink-0 rounded-sm bg-[linear-gradient(135deg,var(--bg)_0_50%,var(--btn)_50%_100%)] shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--fg)_14%,transparent)]",
				dark && "dark",
				className,
			)}
		/>
	);
}

function ThemeHalf({ color, dark, split }: { color: ColorTheme; dark: boolean; split: boolean }) {
	return (
		<span
			data-theme={color}
			className={cn("flex min-w-0 flex-1 bg-background", dark && "dark", dark && split && "border-l border-line2")}
		>
			<span className="w-[28%] bg-panel" />
			<span className="flex flex-1 flex-col gap-[5px] p-[9px]">
				<span className="h-1 w-[70%] rounded-xs bg-foreground opacity-85" />
				<span className="h-1 w-[40%] rounded-xs bg-foreground opacity-50" />
				<span className="flex-1 rounded-sm bg-panel" />
				<span className="h-[9px] w-[34%] self-end rounded-xs bg-primary" />
			</span>
		</span>
	);
}

function ThemePreview({ color, mode = "system", className }: { color: ColorTheme; mode?: Mode; className?: string }) {
	const split = mode === "system";
	return (
		<span aria-hidden className={cn("flex h-[76px] overflow-hidden rounded-md border border-line", className)}>
			{mode !== "dark" && <ThemeHalf color={color} dark={false} split={split} />}
			{mode !== "light" && <ThemeHalf color={color} dark split={split} />}
		</span>
	);
}

function ModeIcon({ mode }: { mode: Mode }) {
	if (mode === "light") return <SunIcon />;
	if (mode === "dark") return <MoonIcon />;
	return <MonitorIcon />;
}

export { ModeIcon, ThemePreview, ThemeSwatch };
