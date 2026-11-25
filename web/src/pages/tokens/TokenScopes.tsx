import { Scope, type Token } from "@gen/loco/token/v1/token_pb";
import { AccessRow } from "./AccessPicker";
import { chipName, nodeKey, resolveNode, type Entities } from "./model";

const SCOPE_LABEL: Record<Scope, string> = {
	[Scope.UNSPECIFIED]: "Unspecified",
	[Scope.READ]: "Read",
	[Scope.WRITE]: "Write",
	[Scope.ADMIN]: "Admin",
	[Scope.MEMBER]: "Member",
};

export function TokenScopeList({ token, entities }: { token: Token; entities: Entities }) {
	const groups = Map.groupBy(token.scopes, (scope) => nodeKey(scope.entityType, scope.entityId));
	return (
		<ul className="overflow-hidden rounded-lg border border-line2 bg-raise [&>li:last-child>div]:border-b-0">
			{[...groups].map(([key, scopes]) => {
				const node = resolveNode(entities, key);
				return (
					<li key={key}>
						<AccessRow
							node={node}
							description={
								<>
									{node.parent !== null && <>{chipName(node.parent)} · </>}
									{node.id}
								</>
							}
						>
							<ul aria-label={`Scopes for ${chipName(node)}`} className="flex flex-wrap gap-1 text-[12px] text-fg2">
								{scopes.map((scope) => (
									<li key={scope.scope} className="rounded-md bg-panel px-2 py-1">
										{SCOPE_LABEL[scope.scope]}
									</li>
								))}
							</ul>
						</AccessRow>
					</li>
				);
			})}
		</ul>
	);
}
