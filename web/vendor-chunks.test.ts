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
		expect(entries["./vendor-react.ts"]).toContain('from "react"');
		expect(entries["./vendor-react.ts"]).toContain('from "react-dom/client"');
		expect(entries["./vendor-tanstack.ts"]).toContain('from "@tanstack/react-query"');
		expect(entries["./vendor-generated.ts"]).toContain('from "@gen/loco/user/v1/user_pb"');
		expect(entries["./vendor-vendor.ts"]).toContain('from "lucide-react"');
		const output = Object.values(entries).join("\n");
		expect(output).not.toContain("@/lib/utils");
		expect(output).not.toContain("loco:env");
	});
});
