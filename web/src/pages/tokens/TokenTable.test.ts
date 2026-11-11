import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { TokenSchema } from "@gen/loco/token/v1/token_pb";

import { TokenTable } from "./TokenTable";

test("token creator uses a member name, preserves an unresolved ID and marks absent attribution", () => {
	const html = renderToStaticMarkup(
		createElement(TokenTable, {
			tokens: [
				create(TokenSchema, { name: "named", createdBy: "member-id" }),
				create(TokenSchema, {
					name: "unresolved",
					createdBy: "former-member-id",
				}),
				create(TokenSchema, { name: "legacy" }),
				create(TokenSchema, { name: "unlabelled", createdBy: "unlabelled-member-id" }),
			],
			creators: new Map([
				["member-id", "Olivia Owner"],
				["unlabelled-member-id", ""],
			]),
			tree: { nodes: new Map(), order: [], childCount: new Map() },
			now: 0,
			isLoading: false,
			empty: null,
			canDuplicate: false,
			canRevoke: false,
			onDuplicate: () => {},
			onRevoke: () => {},
		}),
	);
	expect(html).toContain("Created by");
	expect(html).toContain("Olivia Owner");
	expect(html).toContain("former-member-id");
	expect(html).toContain("Unknown");
	expect(html).toContain(">unlabelled-member-id<");
});
