import { hasStoredSession } from "./auth/session";
import { applyTheme, pageTheme, readColorTheme, readMode, resolveMode, systemPrefersDark } from "./lib/theme";

const page = pageTheme(
	window.location.pathname,
	hasStoredSession(),
	resolveMode(readMode(), systemPrefersDark()),
	readColorTheme(),
);
applyTheme(page.mode, page.color);
