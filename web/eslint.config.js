import eslintReact from "@eslint-react/eslint-plugin";
import { defineConfig, globalIgnores } from "eslint/config";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default defineConfig([
	globalIgnores(["dist", "src/components/ui"]),
	{
		files: ["**/*.{ts,tsx}"],
		extends: [tseslint.configs.strictTypeCheckedOnly, tseslint.configs.stylisticTypeCheckedOnly],
		languageOptions: {
			parser: tseslint.parser,
			parserOptions: {
				projectService: true,
				tsconfigRootDir: import.meta.dirname,
			},
		},
		linterOptions: {
			reportUnusedDisableDirectives: "error",
			reportUnusedInlineConfigs: "error",
		},
		rules: {
			"@typescript-eslint/promise-function-async": "error",
			"@typescript-eslint/restrict-template-expressions": ["error", { allowNumber: true }],
			"@typescript-eslint/return-await": ["error", "always"],
			"@typescript-eslint/switch-exhaustiveness-check": [
				"error",
				{ allowDefaultCaseForExhaustiveSwitch: false, considerDefaultExhaustiveForUnions: false },
			],
		},
	},
	{
		files: ["src/**/*.{ts,tsx}"],
		extends: [
			reactHooks.configs.flat["recommended-latest"],
			eslintReact.configs["strict-type-checked"],
			eslintReact.configs["disable-conflict-eslint-plugin-react-hooks"],
		],
		rules: {
			"@eslint-react/no-array-index-key": "off",
			"@eslint-react/no-unstable-context-value": "off",
			"@eslint-react/set-state-in-effect": "off",
		},
	},
	{
		files: ["src/**/*.{ts,tsx}"],
		ignores: ["src/components/design/**"],
		rules: {
			"no-restricted-imports": [
				"error",
				{
					patterns: [
						{
							group: ["@/components/ui/*"],
							message:
								'Import from "@/components/design/<Component>" instead. components/ui/* is vendored from shadcn and is overwritten by the CLI; add a wrapper in components/design/ if one does not exist yet.',
						},
					],
				},
			],
		},
	},
]);
