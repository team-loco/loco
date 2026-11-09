import { applyTheme, readColorTheme, readMode, resolveMode, systemPrefersDark } from "./lib/theme";

applyTheme(resolveMode(readMode(), systemPrefersDark()), readColorTheme());
