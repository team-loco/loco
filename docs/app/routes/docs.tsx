import type { Route } from "./+types/docs";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { DocsBody, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import { docs } from "@/lib/source";
import { source } from "@/lib/source.server";
import { baseOptions } from "@/lib/layout.shared";
import { useFumadocsLoader } from "fumadocs-core/source/client";
import { getMDXComponents } from "@/components/mdx";
import { use } from "react";
import { Link } from "react-router";
import NotFound from "./not-found";

export async function loader({ params }: Route.LoaderArgs) {
  const page = source.getPage((params["*"] || "").split("/").filter(Boolean));
  if (!page) throw new Response("Not found", { status: 404 });
  return {
    path: page.path,
    url: page.url,
    pageTree: await source.serializePageTree(source.getPageTree()),
  };
}

export default function Page({ loaderData }: Route.ComponentProps) {
  const { path, url, pageTree } = useFumadocsLoader(loaderData);
  const page = docs.getPage(path);
  if (!page) throw new Error(`unknown page: ${path}`);
  const { toc } = use(page.load());
  const Mdx = page.body;
  const canonical = `${import.meta.env.VITE_DOCS_URL}${url === "/" ? "" : `${url.slice(1)}/`}`;

  return (
    <DocsLayout
      {...baseOptions()}
      tree={pageTree}
      sidebar={{ defaultOpenLevel: 2 }}
    >
      {import.meta.env.VITE_DOCS_ENVIRONMENT === "staging" && (
        <div className="staging-banner">
          Staging documentation ·{" "}
          <a href="https://docs.loco.build/">Read production docs</a>
        </div>
      )}
      <DocsPage toc={toc} tableOfContent={{ style: "normal" }}>
        <title>{page.title} | Loco docs</title>
        <link rel="canonical" href={canonical} />
        <DocsTitle>{page.title}</DocsTitle>
        <a
          href={`${import.meta.env.BASE_URL}${path}`}
          className="text-sm text-fd-muted-foreground"
        >
          View Markdown
        </a>
        <DocsBody>
          <Mdx
            components={getMDXComponents({
              a: ({ href, ...props }) => {
                if (href && !/^(?:[a-z]+:|\/\/|#)/i.test(href)) {
                  const target = new URL(
                    href,
                    `https://docs.loco.build/${path}`,
                  );
                  if (!target.pathname.endsWith(".md")) {
                    return (
                      <a
                        href={`${import.meta.env.BASE_URL}${target.pathname.slice(1)}${target.hash}`}
                        {...props}
                      />
                    );
                  }
                  const to =
                    target.pathname
                      .replace(/\/index\.md$/, "/")
                      .replace(/\.md$/, "") + target.hash;
                  return <Link to={to} {...props} />;
                }
                return <a href={href} {...props} />;
              },
            })}
          />
        </DocsBody>
      </DocsPage>
    </DocsLayout>
  );
}

export function ErrorBoundary() {
  return <NotFound />;
}
