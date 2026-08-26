import tseslint from "typescript-eslint";

// Only type-aware rules — oxlint handles everything else.
export default [
	{
	files: ["**/*.{ts,tsx}"],
	ignores: ["dist"],
	languageOptions: {
		parser: tseslint.parser,
		parserOptions: {
			project: ["./tsconfig.node.json", "./tsconfig.app.json"],
			tsconfigRootDir: import.meta.dirname,
		},
	},
	plugins: {
		"@typescript-eslint": tseslint.plugin,
	},
	rules: {
		// Async / Promise correctness
		"@typescript-eslint/await-thenable": "error",
		"@typescript-eslint/no-floating-promises": "error",
		"@typescript-eslint/no-misused-promises": "error",
		"@typescript-eslint/promise-function-async": "error",
		"@typescript-eslint/require-await": "error",
		"@typescript-eslint/return-await": ["error", "always"],

		// Unsafe access
		"@typescript-eslint/no-unsafe-argument": "error",
		"@typescript-eslint/no-unsafe-assignment": "error",
		"@typescript-eslint/no-unsafe-call": "error",
		"@typescript-eslint/no-unsafe-enum-comparison": "error",
		"@typescript-eslint/no-unsafe-member-access": "error",
		"@typescript-eslint/no-unsafe-return": "error",
		"@typescript-eslint/no-unsafe-unary-minus": "error",
		"@typescript-eslint/unbound-method": "error",

		// Unnecessary / redundant type constructs
		"@typescript-eslint/no-base-to-string": "error",
		"@typescript-eslint/no-confusing-void-expression": "error",
		"@typescript-eslint/no-duplicate-type-constituents": "error",
		"@typescript-eslint/no-implied-eval": "error",
		"@typescript-eslint/no-mixed-enums": "error",
		"@typescript-eslint/no-redundant-type-constituents": "error",
		"@typescript-eslint/no-unnecessary-boolean-literal-compare": "error",
		"@typescript-eslint/no-unnecessary-condition": "error",
		"@typescript-eslint/no-unnecessary-template-expression": "error",
		"@typescript-eslint/no-unnecessary-type-arguments": "error",
		"@typescript-eslint/no-unnecessary-type-assertion": "error",
		"@typescript-eslint/restrict-plus-operands": "error",
		"@typescript-eslint/restrict-template-expressions": "error",
		"@typescript-eslint/switch-exhaustiveness-check": "error",

		// Throw / error handling
		"@typescript-eslint/only-throw-error": "error",
		"@typescript-eslint/prefer-promise-reject-errors": "error",
		"@typescript-eslint/use-unknown-in-catch-callback-variable": "error",

		// Stylistic (type-aware)
		"@typescript-eslint/no-for-in-array": "error",
		"@typescript-eslint/prefer-includes": "error",
		"@typescript-eslint/prefer-nullish-coalescing": "error",
		"@typescript-eslint/prefer-optional-chain": "error",
		"@typescript-eslint/prefer-reduce-type-parameter": "error",
		"@typescript-eslint/prefer-regexp-exec": "error",
		"@typescript-eslint/prefer-return-this-type": "error",
		"@typescript-eslint/prefer-string-starts-ends-with": "error",
	},
}];
