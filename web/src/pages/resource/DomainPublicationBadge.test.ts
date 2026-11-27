import { create } from "@bufbuild/protobuf";
import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { DomainPublicationSchema, PublicationState } from "@gen/loco/domain/v1/domain_pb";
import { DomainPublicationBadge } from "./DomainPublicationBadge";

test("publication shows pending, published, error and operator ownership independently", () => {
	for (const [state, label] of [
		[PublicationState.PENDING, "Routing pending"],
		[PublicationState.PUBLISHED, "Routing published"],
		[PublicationState.ERROR, "Routing error"],
		[PublicationState.OPERATOR_MANAGED, "Operator-managed routing"],
	] as const) {
		const publication = create(DomainPublicationSchema, { state, message: "Provider temporarily unavailable" });
		const html = renderToStaticMarkup(createElement(DomainPublicationBadge, { publication }));
		expect(html).toContain(label);
		expect(html).toContain('title="Provider temporarily unavailable"');
	}
});
