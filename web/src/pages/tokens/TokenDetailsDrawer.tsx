import type { Token } from "@gen/loco/token/v1/token_pb";

import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle, SheetDescription } from "@/components/design/Sheet";
import { LEVEL_LABEL } from "@/hooks/useMyScopes";

import { accessChips, chipName, ALLOWS, type Entities } from "./model";

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
					<p className="text-[13px] text-fg3">
						All scopes granted to this token. Access to an organization or workspace includes the resources inside it.
					</p>
					<ul className="flex flex-col gap-4">
						{token !== null &&
							accessChips(entities, token).map(({ key, node, level }) => (
								<li key={key} className="rounded-lg border border-line p-3">
									<div className="flex flex-wrap items-baseline justify-between gap-2">
										<span className="min-w-0 font-mono text-[13px] [overflow-wrap:anywhere]">{chipName(node)}</span>
										<span className="text-[12px] text-fg2">{LEVEL_LABEL[level]}</span>
									</div>
									{node.parent !== null && (
										<p className="mt-1 text-[12px] text-fg3 [overflow-wrap:anywhere]">{chipName(node.parent)}</p>
									)}
									<ul className="mt-2 list-disc space-y-1 pl-4 text-[12px] text-fg2">
										{ALLOWS[node.kind].slice(0, level).map((allow) => (
											<li key={allow}>{allow}</li>
										))}
									</ul>
								</li>
							))}
					</ul>
				</SheetBody>
			</SheetContent>
		</Sheet>
	);
}
