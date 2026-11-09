import { createContext } from "react";

import type { ColorTheme, ResolvedTheme } from "./theme";

export interface ThemeContextType {
	theme: ResolvedTheme;
	toggle: () => void;
	color: ColorTheme;
	setColor: (color: ColorTheme) => void;
}

export const ThemeContext = createContext<ThemeContextType | undefined>(undefined);
