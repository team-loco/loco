import { applyTheme, readMode, resolveMode, systemPrefersDark } from "./lib/theme";

applyTheme(resolveMode(readMode(), systemPrefersDark()));
