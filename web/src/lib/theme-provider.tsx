import { useEffect, useState, useSyncExternalStore } from "react";

import {
	applyTheme,
	readColorTheme,
	readMode,
	resolveMode,
	storeColorTheme,
	storeMode,
	subscribeSystemTheme,
	systemPrefersDark,
	toggledMode,
	type ColorTheme,
	type Mode,
} from "./theme";
import { ThemeContext } from "./theme-context";

const THEME_SOUND_VOLUME = 0.9;

function playThemeSound(toDark: boolean) {
	const audio = new Audio(toDark ? "/darkMode.wav" : "/lightMode.wav");
	audio.volume = THEME_SOUND_VOLUME;
	void audio.play().catch(() => undefined);
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
	const [mode, setMode] = useState<Mode>(readMode);
	const [color, setColor] = useState<ColorTheme>(readColorTheme);
	const systemDark = useSyncExternalStore(subscribeSystemTheme, systemPrefersDark);
	const resolved = resolveMode(mode, systemDark);

	useEffect(() => {
		applyTheme(resolved, color);
	}, [resolved, color]);

	const toggle = () => {
		const next = toggledMode(mode, systemDark);
		setMode(next);
		storeMode(next);
		playThemeSound(resolved !== "dark");
	};

	const changeColor = (next: ColorTheme) => {
		setColor(next);
		storeColorTheme(next);
	};

	const contextValue = { theme: resolved, toggle, color, setColor: changeColor };

	return <ThemeContext value={contextValue}>{children}</ThemeContext>;
}
