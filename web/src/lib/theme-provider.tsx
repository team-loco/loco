import { useLayoutEffect, useState, useSyncExternalStore } from "react";
import { useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";

import {
	applyTheme,
	pageTheme,
	readColorTheme,
	readMode,
	resolveMode,
	storeColorTheme,
	storeMode,
	subscribeSystemTheme,
	systemPrefersDark,
	type ColorTheme,
	type Mode,
} from "./theme";
import { ThemeContext } from "./theme-context";
import { ThemeStylesheet } from "./theme-stylesheet";

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
	const { pathname } = useLocation();
	const { signedOut } = useAuth();
	const resolved = resolveMode(mode, systemDark);
	const page = pageTheme(pathname, !signedOut, resolved, color);

	useLayoutEffect(() => {
		applyTheme(page.mode, page.color);
	}, [page.mode, page.color]);

	const changeMode = (next: Mode) => {
		setMode(next);
		storeMode(next);
		const nextResolved = resolveMode(next, systemDark);
		if (nextResolved !== resolved) playThemeSound(nextResolved === "dark");
	};

	const changeColor = (next: ColorTheme) => {
		setColor(next);
		storeColorTheme(next);
	};

	const contextValue = { theme: page.mode, mode, setMode: changeMode, color, setColor: changeColor };

	return (
		<ThemeContext value={contextValue}>
			<ThemeStylesheet color={page.color} />
			{children}
		</ThemeContext>
	);
}
