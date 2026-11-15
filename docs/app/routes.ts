import { index, route, type RouteConfig } from "@react-router/dev/routes";

export default [
  route("llms.txt", "routes/llms.txt.ts"),
  route("llms-full.txt", "routes/llms-full.txt.ts"),
  route("sitemap.xml", "routes/sitemap.xml.ts"),
  route("robots.txt", "routes/robots.txt.ts"),
  index("routes/docs.tsx", { id: "overview" }),
  route("api/search", "routes/search.ts"),
  route("404", "routes/not-found.tsx"),
  route("*", "routes/docs.tsx"),
] satisfies RouteConfig;
