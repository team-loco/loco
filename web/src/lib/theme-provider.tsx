import { useEffect, useState } from "react";
import { readStorage, writeStorage } from "./storage";
import { ThemeContext, type Theme } from "./theme-context";

const THEME_KEY = "loco:theme:v1";

export function ThemeProvider({ children }: { children: React.ReactNode }) {
	const [theme, setTheme] = useState<Theme>(() => {
		const stored = readStorage(THEME_KEY);
		if (stored === "light" || stored === "dark") {
			return stored;
		}
		return window.matchMedia("(prefers-color-scheme: dark)").matches
			? "dark"
			: "light";
	});

	useEffect(() => {
		const root = document.documentElement;
		if (theme === "dark") {
			root.classList.add("dark");
		} else {
			root.classList.remove("dark");
		}
		writeStorage(THEME_KEY, theme);
	}, [theme]);

	const toggleTheme = () => {
		setTheme((prev) => (prev === "light" ? "dark" : "light"));
	};

	const contextValue = { theme, toggleTheme };

	return (
		<ThemeContext value={contextValue}>
			{children}
		</ThemeContext>
	);
}
