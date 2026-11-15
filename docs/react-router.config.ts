import type { Config } from "@react-router/dev/config";
import { glob } from "node:fs/promises";
import { getSlugs } from "fumadocs-core/source";

const basename = process.env.DOCS_BASE_PATH || "/";

export default {
  ssr: false,
  routeDiscovery: { mode: "initial" },
  basename,
  async prerender() {
    const paths = [
      "/api/search",
      "/404",
      "/llms.txt",
      "/llms-full.txt",
      "/sitemap.xml",
      "/robots.txt",
    ];
    for await (const entry of glob("**/*.md", { cwd: "content" })) {
      paths.push(`/${getSlugs(entry).join("/")}`);
    }
    return paths;
  },
} satisfies Config;
