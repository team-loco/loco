/// <reference types="vite/client" />
/// <reference types="vite-plugin-svgr/client" />

// Vite's own ImportMetaEnv has an `[key: string]: any` index signature, which
// makes every VITE_* read an `any`. Declaring the ones we actually use gives
// them real types and keeps the no-unsafe-* rules meaningful at this boundary.
interface ImportMetaEnv {
	readonly VITE_API_URL?: string;
	readonly VITE_APP_ENV?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}
