import { createContext } from "react";

import type { ResolvedTheme } from "./theme";

export interface ThemeContextType {
	resolved: ResolvedTheme;
	toggle: () => void;
}

export const ThemeContext = createContext<ThemeContextType | undefined>(undefined);
