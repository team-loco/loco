import { useState } from "react";
import { type ColumnDef } from "@tanstack/react-table";
import { type Token } from "@/gen/token/v1/token_pb";
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
import { Trash2, Shield, ShieldCheck, ShieldAlert } from "lucide-react";
import { EntityType, Scope } from "@/gen/token/v1/token_pb";

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

// Entity type display helpers - using badge variants
const entityTypeDisplay: Record<
	number,
	{
		label: string;
		variant: "neo-blue" | "neo-purple" | "neo-green" | "neo-orange" | "neo-red";
	}
> = {
	[EntityType.USER]: {
		label: "User",
		variant: "neo-blue",
	},
	[EntityType.ORGANIZATION]: {
		label: "Organization",
		variant: "neo-purple",
	},
	[EntityType.WORKSPACE]: {
		label: "Workspace",
		variant: "neo-green",
	},
	[EntityType.RESOURCE]: {
		label: "Resource",
		variant: "neo-orange",
	},
	[EntityType.SYSTEM]: {
		label: "System",
		variant: "neo-red",
	},
};

// Scope display helpers - using badge variants
const scopeDisplay: Record<
	number,
	{
		label: string;
		icon: typeof Shield;
		variant: "neo-gray" | "neo-blue" | "neo-red";
	}
> = {
	[Scope.READ]: {
		label: "Read",
		icon: Shield,
		variant: "neo-gray",
	},
	[Scope.WRITE]: {
		label: "Write",
		icon: ShieldCheck,
		variant: "neo-blue",
	},
	[Scope.ADMIN]: {
		label: "Admin",
		icon: ShieldAlert,
		variant: "neo-red",
	},
};

interface ActionsCellProps {
	token: Token;
	onRevokeToken: (tokenName: string) => void;
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
						<AlertDialogDescription>
							Are you sure you want to revoke the token "{token.name}"? This
							action cannot be undone and any applications using this token will
							lose access immediately.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<div className="flex gap-2 justify-end">
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => {
								onRevokeToken(token.name);
								setOpen(false);
							}}
							className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
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
	onRevokeToken: (tokenName: string) => void,
	isRevoking: boolean
): ColumnDef<Token>[] {
	return [
		{
			accessorKey: "name",
			header: "Token Name",
			cell: ({ row }) => {
				const token = row.original;
				const createdDate = token.createdAt
					? new Date(Number(token.createdAt.seconds) * 1000)
					: null;
				return (
					<div className="flex flex-col gap-0.5">
						<span className="font-medium text-xs">{token.name}</span>
						{createdDate && (
							<span className="text-[10px] text-muted-foreground">
								{createdDate.toLocaleDateString()}{" "}
								{createdDate.toLocaleTimeString([], {
									hour: "2-digit",
									minute: "2-digit",
								})}
							</span>
						)}
					</div>
				);
			},
		},
		{
			id: "scopes",
			header: "Permissions",
			cell: ({ row }) => {
				const token = row.original;
				const scopeGroups = new Map<number, Set<number>>();

				// Group scopes by entity type
				token.scopes.forEach((scope) => {
					if (!scopeGroups.has(scope.entityType)) {
						scopeGroups.set(scope.entityType, new Set());
					}
					scopeGroups.get(scope.entityType)!.add(scope.scope);
				});

				return (
					<div className="flex flex-wrap gap-1">
						{Array.from(scopeGroups.entries()).map(([entityType, scopes]) => {
							const entityInfo = entityTypeDisplay[entityType];
							const scopeList = Array.from(scopes);

							return (
								<TooltipProvider key={entityType}>
									<Tooltip>
										<TooltipTrigger>
											<Badge
												variant={entityInfo.variant}
												className="h-5 text-[10px] px-1.5 py-0"
											>
												{entityInfo.label}: {scopeList.length} scope
												{scopeList.length !== 1 ? "s" : ""}
											</Badge>
										</TooltipTrigger>
										<TooltipContent neo>
											<div className="space-y-1">
												{scopeList.map((scope) => {
													const scopeInfo = scopeDisplay[scope];
													const ScopeIcon = scopeInfo.icon;
													return (
														<div
															key={scope}
															className="flex items-center gap-2 text-xs"
														>
															<ScopeIcon className="h-3 w-3" />
															{scopeInfo.label}
														</div>
													);
												})}
											</div>
										</TooltipContent>
									</Tooltip>
								</TooltipProvider>
							);
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
