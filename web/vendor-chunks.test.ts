import { describe, expect, test } from "bun:test";

import { collectImports, vendorEntries } from "./vendor-chunks";

describe("collectImports", () => {
	test("records the names each package is imported and re-exported by", () => {
		const imported = collectImports(
			[
				`import React, { useState, type FC, useMemo as memo } from "react";
import * as Dialog from "@base-ui/react/dialog";
import type { QueryClient } from "@tanstack/react-query";
export { cn } from "cn";
export * from "sonner";
import { local } from "./local";
import { Button } from "@/components/design/Button";
import "./index.css";`,
			],
			["react/jsx-runtime"],
		);
		expect(Object.fromEntries([...imported].map(([spec, names]) => [spec, [...names].toSorted()]))).toEqual({
			"react/jsx-runtime": ["*"],
			react: ["default", "useMemo", "useState"],
			"@base-ui/react/dialog": ["*"],
			cn: ["cn"],
			sonner: ["*"],
			"@/components/design/Button": ["Button"],
		});
	});
});

describe("vendorEntries", () => {
	test("groups packages and generated code, and leaves app aliases and loco:env to the app", () => {
		const entries = vendorEntries(
			new Map([
				["react", new Set(["default", "useState"])],
				["react-dom/client", new Set(["createRoot"])],
				["@tanstack/react-query", new Set(["useQuery"])],
				["lucide-react", new Set(["BoxIcon"])],
				["@/lib/utils", new Set(["cn"])],
				["@gen/loco/user/v1/user_pb", new Set(["UserSchema"])],
				["loco:env", new Set(["PUBLIC_API_URL"])],
			]),
		);
		expect(entries).toEqual({
			"./vendor-react.ts": [
				'export { default as v0, useState as v1 } from "react";',
				'export { createRoot as v2 } from "react-dom/client";',
			].join("\n"),
			"./vendor-base-ui.ts": "",
			"./vendor-tanstack.ts": 'export { useQuery as v0 } from "@tanstack/react-query";',
			"./vendor-recharts.ts": "",
			"./vendor-sugar-high.ts": "",
			"./vendor-generated.ts": 'export { UserSchema as v0 } from "@gen/loco/user/v1/user_pb";',
			"./vendor-vendor.ts": 'export { BoxIcon as v0 } from "lucide-react";',
		});
	});
});
