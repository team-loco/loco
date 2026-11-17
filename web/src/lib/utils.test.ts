import { expect, test } from "bun:test";

import { cn as cnImport } from "cn";

import { cn, formatShortId } from "./utils";

test("cn lets a theme radius replace a stock radius", () => {
	expect(cn("rounded-lg p-1", "rounded-control")).toBe("p-1 rounded-control");
	expect(cn("rounded-control", "rounded-control-inner")).toBe("rounded-control-inner");
	expect(cn("rounded-lg", "rounded-card")).toBe("rounded-card");
	expect(cn("md:data-[x=y]:rounded-xl", "md:data-[x=y]:rounded-overlay")).toBe("md:data-[x=y]:rounded-overlay");
});

test("the cn package import resolves to the theme-aware helper", () => {
	expect(cnImport("rounded-full", "rounded-card")).toBe("rounded-card");
});

test("short IDs distinguish deployments created in the same UUIDv7 timestamp bucket", () => {
	const ids = ["019ab668-1111-7000-8000-000011112222", "019ab668-1112-7000-8000-000033334444"];
	expect(ids.map(formatShortId)).toEqual(["11112222", "33334444"]);
});
