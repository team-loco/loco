import { createContext } from "react";

import type { ColorTheme, Mode, ResolvedTheme } from "./theme";

export interface ThemeContextType {
	theme: ResolvedTheme;
	mode: Mode;
	setMode: (mode: Mode) => void;
	toggle: () => void;
	color: ColorTheme;
	setColor: (color: ColorTheme) => void;
}

export const ThemeContext = createContext<ThemeContextType | undefined>(undefined);
