import type { ColorTheme } from "@/lib/theme";
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

function ThemeHalf({ color, dark }: { color: ColorTheme; dark: boolean }) {
	return (
		<span data-theme={color} className={cn("flex min-w-0 flex-1 bg-background", dark && "dark border-l border-line2")}>
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

function ThemePreview({ color, className }: { color: ColorTheme; className?: string }) {
	return (
		<span aria-hidden className={cn("flex h-[76px] overflow-hidden rounded-md border border-line", className)}>
			<ThemeHalf color={color} dark={false} />
			<ThemeHalf color={color} dark />
		</span>
	);
}

export { ThemePreview, ThemeSwatch };
