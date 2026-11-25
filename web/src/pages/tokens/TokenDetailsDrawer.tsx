import { Scope, type Token } from "@gen/loco/token/v1/token_pb";

import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle, SheetDescription } from "@/components/design/Sheet";

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
		<ul className="flex flex-col gap-4">
			{[...groups].map(([key, scopes]) => {
				const node = resolveNode(entities, key);
				return (
					<li key={key} className="rounded-lg border border-line p-3">
						<span className="font-mono text-[13px] [overflow-wrap:anywhere]">{chipName(node)}</span>
						{node.parent !== null && (
							<p className="mt-1 text-[12px] text-fg3 [overflow-wrap:anywhere]">{chipName(node.parent)}</p>
						)}
						<p className="mt-1 font-mono text-[11px] text-fg3 [overflow-wrap:anywhere]">{node.id}</p>
						<ul aria-label={`Scopes for ${chipName(node)}`} className="mt-2 flex flex-wrap gap-2 text-[12px] text-fg2">
							{scopes.map((scope) => (
								<li key={scope.scope} className="rounded-md bg-panel px-2 py-1">
									{SCOPE_LABEL[scope.scope]}
								</li>
							))}
						</ul>
					</li>
				);
			})}
		</ul>
	);
}

export function TokenDetailsDrawer({
	token,
	entities,
	onClose,
}: {
	token: Token | null;
	entities: Entities;
	onClose: () => void;
}) {
	return (
		<Sheet
			open={token !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent>
				<SheetHeader>
					<SheetTitle>Token access</SheetTitle>
				</SheetHeader>
				<SheetBody>
					<SheetDescription className="break-all">{token?.name}</SheetDescription>
					<p className="text-[13px] text-fg3">Every scope granted to this token, grouped by its target.</p>
					{token !== null && <TokenScopeList token={token} entities={entities} />}
				</SheetBody>
			</SheetContent>
		</Sheet>
	);
}
