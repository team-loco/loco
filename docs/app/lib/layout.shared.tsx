import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

import logo from "../../../web/public/logo.svg?url";

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <img src={logo} alt="Loco" className="h-7 w-auto" />
          <span className="text-sm text-fd-muted-foreground">Docs</span>
        </>
      ),
      url: "/",
    },
    githubUrl: "https://github.com/team-loco/loco",
  };
}
