import { source } from "@/lib/source.server";

export function loader() {
  const urls = source
    .getPages()
    .map(
      (page) =>
        `<url><loc>${new URL(page.url === "/" ? "" : `${page.url.slice(1)}/`, import.meta.env.VITE_DOCS_URL).href}</loc></url>`,
    )
    .join("");
  return new Response(
    `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${urls}</urlset>`,
    { headers: { "Content-Type": "application/xml" } },
  );
}
