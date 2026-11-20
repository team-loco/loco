import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { TokenSchema } from "@gen/loco/token/v1/token_pb";

import { CreatorCell } from "./TokenTable";

test("token creator uses a member name, preserves an unresolved ID and marks absent attribution", () => {
	const creators = new Map([
		["member-id", { name: "Olivia Owner", avatarUrl: "" }],
		["unlabelled-member-id", { name: "", avatarUrl: "" }],
	]);
	const html = [
		create(TokenSchema, { name: "named", createdBy: "member-id" }),
		create(TokenSchema, { name: "unresolved", createdBy: "former-member-id" }),
		create(TokenSchema, { name: "legacy" }),
		create(TokenSchema, { name: "unlabelled", createdBy: "unlabelled-member-id" }),
	]
		.map((token) =>
			renderToStaticMarkup(createElement(CreatorCell, { token, creator: creators.get(token.createdBy), now: 0 })),
		)
		.join("");
	expect(html).toContain("Olivia Owner");
	expect(html).toContain("former-member-id");
	expect(html).toContain("Unknown");
	expect(html).toContain(">unlabelled-member-id<");
});
