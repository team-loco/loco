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
	};

	const contextValue = { theme: resolved, toggle };

	return <ThemeContext value={contextValue}>{children}</ThemeContext>;
}
