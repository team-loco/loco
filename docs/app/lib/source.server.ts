import { llms, loader } from "fumadocs-core/source";
import { docs } from "./source";

export const source = loader({ source: docs.toFumadocsSource(), baseUrl: "/" });

export const docsLlms = llms(source, {
  section: () => false,
  renderPage: async (page) => `# ${page.data.title}

${await page.data.getText("processed")}`,
  renderUrl: (item) => new URL(item.url, import.meta.env.VITE_DOCS_URL).href,
});
