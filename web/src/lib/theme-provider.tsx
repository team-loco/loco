import { useEffect, useState, useSyncExternalStore } from "react";

import {
	applyTheme,
	readMode,
	resolveMode,
	storeMode,
	subscribeSystemTheme,
	systemPrefersDark,
	toggledMode,
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
	const systemDark = useSyncExternalStore(subscribeSystemTheme, systemPrefersDark);
	const resolved = resolveMode(mode, systemDark);

	useEffect(() => {
		applyTheme(resolved);
	}, [resolved]);

	const toggle = () => {
		const next = toggledMode(mode, systemDark);
		setMode(next);
		storeMode(next);
		playThemeSound(resolved !== "dark");
	};

	const contextValue = { theme: resolved, toggle };

	return <ThemeContext value={contextValue}>{children}</ThemeContext>;
}
