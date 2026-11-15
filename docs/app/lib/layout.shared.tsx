import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <img
            src={`${import.meta.env.BASE_URL}assets/logo.svg`}
            alt="Loco"
            className="h-7 w-auto"
          />
          <span className="text-sm text-fd-muted-foreground">Docs</span>
        </>
      ),
      url: "/",
    },
    githubUrl: "https://github.com/team-loco/loco",
  };
}
