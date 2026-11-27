import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { EntityType, Scope, TokenSchema } from "@gen/loco/token/v1/token_pb";

import { entityNode, withScopeEntities } from "./model";
import { TokenScopeList } from "./TokenScopes";

test("token details show every target and exact independent scopes", () => {
	const nodes = ["cart", "orders", "payments", "inventory"].map((name) =>
		entityNode(EntityType.RESOURCE, name, name, null),
	);
	const scopes = nodes.map((node) => ({
		entityType: node.entityType,
		entityId: node.id,
		scope: Scope.WRITE,
	}));
	const token = create(TokenSchema, { scopes });
	const entities = new Map(nodes.map((node) => [node.key, node]));
	const html = renderToStaticMarkup(createElement(TokenScopeList, { token, entities }));
	for (const name of ["cart", "orders", "payments", "inventory"]) expect(html).toContain(`Scopes for ${name}`);
	expect(html.match(/>Write</g)).toHaveLength(4);
	expect(html).not.toContain(">Read<");
	expect(html).not.toContain(">Admin<");
});

test("token details preserve admin-only, member and combined grants and full unknown IDs", () => {
	const id = "00000000-0000-7000-8000-000000000099";
	const token = create(TokenSchema, {
		scopes: [{ entityType: EntityType.WORKSPACE, entityId: id, scope: Scope.ADMIN }],
	});
	const entities = withScopeEntities(new Map(), token.scopes);
	const render = () => renderToStaticMarkup(createElement(TokenScopeList, { token, entities }));
	expect(render()).toContain(id);
	expect(render()).toContain(">Admin<");
	expect(render()).not.toContain(">Read<");
	expect(render()).not.toContain(">Write<");
	token.scopes.push({ ...token.scopes[0]!, scope: Scope.READ }, { ...token.scopes[0]!, scope: Scope.MEMBER });
	const html = render();
	expect(html).toContain(">Read<");
	expect(html).toContain(">Member<");
	expect(html.match(/aria-label="Scopes for/g)).toHaveLength(1);
});

test("routing publisher grants retain their identity and exact scopes", () => {
	const token = create(TokenSchema, {
		scopes: [
			{
				entityType: EntityType.ROUTING_PUBLISHER,
				entityId: "publisher-id",
				scope: Scope.READ,
			},
			{
				entityType: EntityType.ROUTING_PUBLISHER,
				entityId: "publisher-id",
				scope: Scope.WRITE,
			},
		],
	});
	const entities = withScopeEntities(new Map(), token.scopes);
	const html = renderToStaticMarkup(createElement(TokenScopeList, { token, entities }));
	expect(html).toContain("routing publisher");
	expect(html).toContain("publisher-id");
	expect(html).toContain(">Read<");
	expect(html).toContain(">Write<");
	expect(html).not.toContain(">Admin<");
});
