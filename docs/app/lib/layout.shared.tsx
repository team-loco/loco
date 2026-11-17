import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

import logo from "../../../web/public/logo.svg?url";

export function baseOptions(): BaseLayoutProps {
  const origin = new URL(import.meta.env.VITE_DOCS_URL!).origin;
  return {
    nav: {
      title: (
        <span className="docs-brand">
          <span
            role="img"
            aria-label="Loco"
            className="docs-logo"
            style={{ maskImage: `url("${logo}")` }}
          />
          <span className="docs-brand-label">Docs</span>
        </span>
      ),
      url: "/",
    },
    links: [
      {
        text: "API reference",
        url: origin.replace("://docs.", "://api."),
        external: true,
      },
      {
        text: "Dashboard",
        url: origin.replace("://docs.", "://"),
        external: true,
      },
    ],
    githubUrl: "https://github.com/team-loco/loco",
  };
}
