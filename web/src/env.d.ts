declare module "loco:env" {
	export const PUBLIC_API_URL: string;
	export const PUBLIC_APP_ENV: string;
}

declare module "*.css" {
	const href: string;
	export default href;
}

declare module "*.woff2" {
	const src: string;
	export default src;
}

interface Window {
	locoConfig?: { apiURL?: string };
}
