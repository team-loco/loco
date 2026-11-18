import { applyTheme, pageTheme, readColorTheme, readMode, resolveMode, systemPrefersDark } from "./lib/theme";

const page = pageTheme(window.location.pathname, false, resolveMode(readMode(), systemPrefersDark()), readColorTheme());
applyTheme(page.mode, page.color);
