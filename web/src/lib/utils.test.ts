import { expect, test } from "bun:test";

import { cn } from "./utils";

test("cn lets a theme radius replace a stock radius", () => {
	expect(cn("rounded-lg p-1", "rounded-control")).toBe("p-1 rounded-control");
	expect(cn("rounded-control", "rounded-control-inner")).toBe("rounded-control-inner");
	expect(cn("rounded-lg", "rounded-card")).toBe("rounded-card");
	expect(cn("md:data-[x=y]:rounded-xl", "md:data-[x=y]:rounded-overlay")).toBe("md:data-[x=y]:rounded-overlay");
});
