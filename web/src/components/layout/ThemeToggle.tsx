import { Switch } from "@/components/ui/switch";
import { useTheme } from "@/lib/use-theme";
import { Moon, Sun } from "lucide-react";

export function ThemeToggle() {
	const { theme, toggleTheme } = useTheme();
	const isDark = theme === "dark";

	return (
		<div className="flex items-center gap-2 px-2 py-2">
			<Sun className="h-4 w-4" />
			<Switch
				id="theme-toggle"
				checked={isDark}
				onCheckedChange={toggleTheme}
				className="data-[state=checked]:bg-main"
			/>
			<Moon className="h-4 w-4" />
		</div>
	);
}
