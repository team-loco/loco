import { resolveDocsURL } from "@/lib/docs-url";

export const GITHUB_URL = "https://github.com/team-loco/loco";
export const DOCS_URL = resolveDocsURL(window.locoConfig?.docsURL);
export const DOCS_GET_STARTED = `${DOCS_URL}/getting-started/install/`;
export const DOCS_LOGS = `${DOCS_URL}/reference/log-queries/`;
export const DOCS_TRACES = `${DOCS_URL}/operations/applications/#trace-sampling`;
export const DOCS_OPERATIONS = `${DOCS_URL}/operations/applications/`;
export const INSTALL_COMMAND = "curl -fsSL https://loco.build/install.sh | sh";
export const EXAMPLES_URL = "https://github.com/team-loco/loco/tree/main/examples";
export const COPIED_MS = 1600;
