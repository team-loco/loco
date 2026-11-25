import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

import { ArrowUpRight } from "lucide-react";
import { useEffect, useState } from "react";

import logo from "../../../web/public/logo.svg?url";

type Version = { name: string; url: string };

const version = import.meta.env.VITE_DOCS_VERSION;
const dashboard =
  import.meta.env.VITE_DOCS_ENVIRONMENT === "staging"
    ? "https://staging.loco.build"
    : "https://loco.build";

// The versions docs.loco.build publishes, read at run time from its root so a
// release's docs list the versions published after it.
export function useVersions() {
  const [versions, setVersions] = useState<Version[]>([]);
  useEffect(() => {
    if (!version) return;
    fetch("/versions.json")
      .then((reply) => reply.json())
      .then(setVersions, () => {});
  }, []);
  return versions;
}

export function baseOptions(versions: Version[] = []): BaseLayoutProps {
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
      ...(versions.length > 0
        ? [
            {
              type: "menu" as const,
              text: version,
              items: versions.map((item) => ({
                type: "custom" as const,
                children: (
                  <a
                    href={item.url}
                    data-active={item.name === version}
                    className="rounded-md p-2 transition-colors hover:bg-fd-accent hover:text-fd-accent-foreground data-[active=true]:text-fd-primary"
                  >
                    {item.name}
                  </a>
                ),
              })),
            },
          ]
        : []),
      {
        text: (
          <span className="docs-external-link">
            API reference
            <ArrowUpRight aria-hidden="true" size={13} />
          </span>
        ),
        url: dashboard.replace("://", "://api."),
        external: true,
      },
      {
        text: (
          <span className="docs-external-link">
            Dashboard
            <ArrowUpRight aria-hidden="true" size={13} />
          </span>
        ),
        url: dashboard,
        external: true,
      },
    ],
    themeSwitch: { mode: "light-dark-system" },
    githubUrl: "https://github.com/team-loco/loco",
  };
}
