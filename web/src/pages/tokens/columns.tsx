import { useState } from "react";
import { type ColumnDef } from "@tanstack/react-table";
import { type Token } from "@/gen/loco/token/v1/token_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/ui/tooltip";

import { Trash2 } from "lucide-react";
import { EntityType } from "@/gen/loco/token/v1/token_pb";
import { formatShortId } from "@/lib/utils";

function formatRelativeTimeFuture(date: Date): string {
	const now = new Date();
	const diffMs = date.getTime() - now.getTime();
	const diffSec = Math.floor(diffMs / 1000);
	const diffMin = Math.floor(diffSec / 60);
	const diffHour = Math.floor(diffMin / 60);
	const diffDay = Math.floor(diffHour / 24);
	const diffMonth = Math.floor(diffDay / 30);

	if (diffSec < 0) return "expired";
	if (diffMin < 60) return `in ${diffMin} minute${diffMin !== 1 ? "s" : ""}`;
	if (diffHour < 24) return `in ${diffHour} hour${diffHour !== 1 ? "s" : ""}`;
	if (diffDay < 30) return `in ${diffDay} day${diffDay !== 1 ? "s" : ""}`;
	return `in ${diffMonth} month${diffMonth !== 1 ? "s" : ""}`;
}

function maskToken(token: string): string {
	if (token.length < 4) return token;
	return token.substring(0, 4) + "...";
}

// Entity type display helpers - using badge variants
const entityTypeDisplay: Record<
	number,
	{
		label: string;
		variant: "info" | "default" | "success" | "warning" | "error";
	}
> = {
	[EntityType.USER]: {
		label: "User",
		variant: "info",
	},
	[EntityType.ORGANIZATION]: {
		label: "Organization",
		variant: "info",
	},
	[EntityType.WORKSPACE]: {
		label: "Workspace",
		variant: "info",
	},
	[EntityType.RESOURCE]: {
		label: "Resource",
		variant: "info",
	},
	[EntityType.SYSTEM]: {
		label: "System",
		variant: "info",
	},
};



interface ActionsCellProps {
	token: Token;
	onRevokeToken: (tokenName: string, tokenEntityType: EntityType, tokenEntityId: string) => void;
	isRevoking: boolean;
}

function ActionsCell({ token, onRevokeToken, isRevoking }: ActionsCellProps) {
	const [open, setOpen] = useState(false);

	return (
		<div className="flex justify-end">
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="h-8 w-8"
						title="Revoke token"
						disabled={isRevoking}
					>
						<Trash2 className="h-4 w-4 text-destructive" />
					</Button>
				</AlertDialogTrigger>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Revoke Token</AlertDialogTitle>
					</AlertDialogHeader>
					<div className="space-y-4">
						<div className="p-3 bg-muted/50 rounded border border-border">
							<div className="text-xs font-semibold text-muted-foreground mb-1">Token</div>
							<div className="text-sm font-medium">{token.name}</div>
						</div>
						{token.createdAt && (
							<div className="p-3 bg-muted/50 rounded border border-border">
								<div className="text-xs font-semibold text-muted-foreground mb-1">Created At</div>
								<div className="text-sm">
									{new Date(Number(token.createdAt.seconds) * 1000).toLocaleString()}
								</div>
							</div>
						)}
						<div className="pt-2 border-t">
							<p className="text-sm text-muted-foreground">
								This action cannot be undone and any applications using this token will lose access immediately.
							</p>
						</div>
					</div>
					<div className="flex gap-2 justify-end">
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => {
								onRevokeToken(token.name, token.entityType, token.entityId);
								setOpen(false);
							}}
							className="bg-destructive text-white hover:bg-destructive/90"
						>
							Revoke Token
						</AlertDialogAction>
					</div>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

export function getTokenColumns(
	onRevokeToken: (tokenName: string, tokenEntityType: EntityType, tokenEntityId: string) => void,
	isRevoking: boolean
): ColumnDef<Token>[] {
	return [
		{
			accessorKey: "name",
			header: "Token Name",
			cell: ({ row }) => {
				const token = row.original;
				return (
					<div className="flex flex-col gap-0.5 max-w-48">
						<span className="font-medium text-sm truncate" title={token.name}>{token.name}</span>
					</div>
				);
			},
		},
		{
			id: "token",
			header: "Token Preview",
			cell: ({ row }) => {
				const token = row.original;
				// Note: The actual token value is only shown at creation time for security
				return (
					<code className="text-xs bg-muted px-2 py-1 rounded font-mono font-semibold border border-border">
						{maskToken(token.name)}
					</code>
				);
			},
		},
		{
			id: "scopes",
			header: "Permissions",
			cell: ({ row }) => {
				const token = row.original;
				const scopeGroups = new Map<number, Map<string, Set<number>>>();

				// Group scopes by entity type and entity ID
				token.scopes.forEach((scope) => {
					if (!scopeGroups.has(scope.entityType)) {
						scopeGroups.set(scope.entityType, new Map());
					}
					const entityMap = scopeGroups.get(scope.entityType)!;
					if (!entityMap.has(scope.entityId)) {
						entityMap.set(scope.entityId, new Set());
					}
					entityMap.get(scope.entityId)!.add(scope.scope);
				});

				return (
					<div className="flex flex-wrap gap-1">
						{Array.from(scopeGroups.entries()).map(([entityType, entityMap]) => {
							const entityInfo = entityTypeDisplay[entityType];
							
							return Array.from(entityMap.entries()).map(([entityId, scopes]) => {
								const scopeList = Array.from(scopes);
								const scopeShortMap: Record<number, string> = {
									0: "?",
									1: "R",
									2: "W",
									3: "A",
								};
								const scopeStr = Array.from(scopeList)
									.sort()
									.map((s) => scopeShortMap[s] || "?")
									.join("");

								return (
									<TooltipProvider key={`${entityType}-${entityId}`}>
										<Tooltip>
											<TooltipTrigger asChild>
												<Badge
													variant={entityInfo.variant}
													className="h-5 text-[10px] px-1.5 py-0 cursor-help"
												>
													{entityInfo.label}: {formatShortId(entityId.toString())} {scopeStr}
												</Badge>
											</TooltipTrigger>
											<TooltipContent>
												{entityInfo.label}: {entityId.toString()}
											</TooltipContent>
										</Tooltip>
									</TooltipProvider>
								);
							});
						})}
					</div>
				);
			},
		},
		{
			accessorKey: "expiresAt",
			header: "Expires",
			cell: ({ row }) => {
				const token = row.original;
				if (!token.expiresAt) {
					return <span className="text-muted-foreground text-xs">Never</span>;
				}

				const expiresDate = new Date(Number(token.expiresAt.seconds) * 1000);
				const now = new Date();
				const isExpired = expiresDate < now;

				return (
					<span
						className={`text-xs ${
							isExpired ? "text-destructive" : "text-foreground"
						}`}
					>
						{isExpired ? "Expired" : formatRelativeTimeFuture(expiresDate)}
					</span>
				);
			},
		},
		{
			id: "actions",
			enableHiding: false,
			cell: ({ row }) => (
				<ActionsCell
					token={row.original}
					onRevokeToken={onRevokeToken}
					isRevoking={isRevoking}
				/>
			),
		},
	];
}
