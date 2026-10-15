declare module "loco:env" {
	export const PUBLIC_API_URL: string;
	export const PUBLIC_APP_ENV: string;
}

declare module "*.css";

interface Window {
	locoConfig?: { apiURL?: string };
}
