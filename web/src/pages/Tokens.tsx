import { useAuth } from "@/auth/AuthProvider";
import {
	AlertDialog,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/design/Card";
import {
	Tooltip,
	TooltipContent,
	TooltipProvider,
	TooltipTrigger,
} from "@/components/design/Tooltip";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { listTokens, revokeToken } from "@gen/loco/token/v1/token-TokenService_connectquery";
import type { Token } from "@gen/loco/token/v1/token_pb";
import { EntityType } from "@gen/loco/token/v1/token_pb";
import { toastConnectError } from "@/lib/error-handler";
import { formatShortId } from "@/lib/utils";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { AlertCircle, Loader2, Plus, Trash2 } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { toast } from "sonner";
import { CreateTokenDialog } from "./tokens/CreateTokenDialog";
import { TokenDisplayDialog } from "./tokens/TokenDisplayDialog";

function formatRelativeTimeFuture(date: Date): string {
	const now = new Date();
	const diffMs = date.getTime() - now.getTime();
	const diffSec = Math.floor(diffMs / 1000);
	const diffMin = Math.floor(diffSec / 60);
	const diffHour = Math.floor(diffMin / 60);
	const diffDay = Math.floor(diffHour / 24);
	const diffMonth = Math.floor(diffDay / 30);

	if (diffSec < 0) return "expired";
	if (diffMin < 60)
		return `in ${diffMin.toString()} minute${diffMin !== 1 ? "s" : ""}`;
	if (diffHour < 24)
		return `in ${diffHour.toString()} hour${diffHour !== 1 ? "s" : ""}`;
	if (diffDay < 30)
		return `in ${diffDay.toString()} day${diffDay !== 1 ? "s" : ""}`;
	return `in ${diffMonth.toString()} month${diffMonth !== 1 ? "s" : ""}`;
}

function TokenCard({
	token,
	onRevokeToken,
	isRevoking,
}: {
	token: Token;
	onRevokeToken: (
		tokenName: string,
		tokenEntityType: EntityType,
		tokenEntityId: string,
	) => void;
	isRevoking: boolean;
}) {
	const [revokeOpen, setRevokeOpen] = useState(false);
	const createdDate = token.createdAt
		? new Date(Number(token.createdAt.seconds) * 1000)
		: null;
	const expiresDate = token.expiresAt
		? new Date(Number(token.expiresAt.seconds) * 1000)
		: null;

	const scopeGroups = new Map<number, Map<string, Set<number>>>();
	token.scopes.forEach((scope) => {
		if (!scopeGroups.has(scope.entityType)) {
			scopeGroups.set(scope.entityType, new Map());
		}
		const entityMap = scopeGroups.get(scope.entityType);
		if (entityMap) {
			if (!entityMap.has(scope.entityId)) {
				entityMap.set(scope.entityId, new Set());
			}
			const scopeSet = entityMap.get(scope.entityId);
			if (scopeSet) {
				scopeSet.add(scope.scope);
			}
		}
	});

	const entityTypeDisplay: Record<
		number,
		{
			label: string;
			variant: "default" | "secondary" | "destructive" | "outline";
		}
	> = {
		[EntityType.USER]: { label: "User", variant: "default" },
		[EntityType.ORGANIZATION]: { label: "Organization", variant: "default" },
		[EntityType.WORKSPACE]: { label: "Workspace", variant: "default" },
		[EntityType.RESOURCE]: { label: "Resource", variant: "default" },
		[EntityType.SYSTEM]: { label: "System", variant: "default" },
	};

	return (
		<div className="bg-white border border-gray-200 rounded-lg p-6 shadow-sm hover:shadow-md transition-shadow">
			{/* Header */}
			<div className="flex justify-between items-start mb-5">
				<h3 className="text-lg font-semibold text-gray-900">{token.name}</h3>

				{/* Actions */}
				<div className="flex gap-2">
					<AlertDialog open={revokeOpen} onOpenChange={setRevokeOpen}>
						<AlertDialogTrigger render={<Button variant="ghost" size="icon" className="h-8 w-8" />}>
							<Trash2 className="w-4 h-4 text-black" />
						</AlertDialogTrigger>
						<AlertDialogContent>
							<AlertDialogHeader>
								<AlertDialogTitle>Revoke Token</AlertDialogTitle>
							</AlertDialogHeader>
							<div className="space-y-4">
								<div className="p-3 bg-gray-50 rounded border border-gray-200">
									<div className="text-xs font-semibold text-gray-500 mb-1">
										Token
									</div>
									<div className="text-sm font-medium text-gray-900">
										{token.name}
									</div>
								</div>
								<p className="text-sm text-gray-600">
									This action cannot be undone and any applications using this
									token will lose access immediately.
								</p>
							</div>
							<div className="flex gap-2 justify-end">
								<AlertDialogCancel>Cancel</AlertDialogCancel>
								<Button
									onClick={() => {
										onRevokeToken(token.name, token.entityType, token.entityId);
										setRevokeOpen(false);
									}}
									disabled={isRevoking}
									variant="outline"
									className="text-destructive hover:text-destructive hover:bg-destructive/5 border-destructive/30"
								>
									{isRevoking ? (
										<>
											<Loader2 className="w-4 h-4 mr-2 animate-spin" />
											Revoking...
										</>
									) : (
										"Revoke Token"
									)}
								</Button>
							</div>
						</AlertDialogContent>
					</AlertDialog>
				</div>
			</div>

			{/* Metadata */}
			<div className="flex flex-wrap gap-6 pt-3">
				{/* Scopes */}
				<div>
					<div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
						Scopes
					</div>
					<div className="flex flex-wrap gap-1">
						{Array.from(scopeGroups.entries()).map(
							([entityType, entityMap]) => {
								return Array.from(entityMap.entries()).map(
									([entityId, scopes]) => {
										const entityInfo = entityTypeDisplay[entityType];
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
											<TooltipProvider
												key={`${entityType.toString()}-${entityId}`}
											>
												<Tooltip>
													<TooltipTrigger>
														<Badge
															variant="secondary"
															className="text-xs cursor-help px-2 py-0.5"
														>
															{entityInfo.label}: {formatShortId(entityId)}{" "}
															{scopeStr}
														</Badge>
													</TooltipTrigger>
													<TooltipContent>
														{entityInfo.label}: {entityId}
													</TooltipContent>
												</Tooltip>
											</TooltipProvider>
										);
									},
								);
							},
						)}
					</div>
				</div>

				{/* Created */}
				<div>
					<div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
						Created
					</div>
					<div className="text-sm text-gray-900">
						{createdDate
							? createdDate.toLocaleDateString("en-US", {
									year: "numeric",
									month: "short",
									day: "numeric",
								})
							: "—"}
					</div>
				</div>

				{/* Last Used */}
				<div>
					<div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
						Last Used
					</div>
					<div className="text-sm text-gray-900">—</div>
				</div>

				{/* Expires */}
				<div>
					<div className="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">
						Expires
					</div>
					<div className="text-sm text-gray-900">
						{!expiresDate ? (
							"Never"
						) : (
							<span
								className={
									expiresDate < new Date() ? "text-red-600" : "text-gray-900"
								}
							>
								{expiresDate < new Date()
									? "Expired"
									: formatRelativeTimeFuture(expiresDate)}
							</span>
						)}
					</div>
				</div>
			</div>
		</div>
	);
}

export function Tokens() {
	const { user } = useAuth();

	const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
	const [newlyCreatedToken, setNewlyCreatedToken] = useState<string | null>(
		null,
	);

	const { activeOrgId } = useOrgWorkspace();

	const {
		data: tokensRes,
		isLoading,
		refetch,
	} = useQuery(
		listTokens,
		user?.id ? { entityType: EntityType.USER, entityId: user.id } : undefined,
		{ enabled: !!user?.id },
	);
	const tokens = useMemo(() => tokensRes?.tokens ?? [], [tokensRes]);

	const { mutate: revokeTokenMutation, isPending: isRevoking } = useMutation(
		revokeToken,
		{
			onSuccess: async () => {
				toast.success("Token revoked successfully");
				await refetch();
			},
			onError: (error) => {
				toastConnectError(error, "Failed to revoke token");
			},
		},
	);

	const handleRevokeToken = useCallback(
		(tokenName: string, tokenEntityType: EntityType, tokenEntityId: string) => {
			revokeTokenMutation({
				name: tokenName,
				entityType: tokenEntityType,
				entityId: tokenEntityId,
			});
		},
		[revokeTokenMutation],
	);

	const handleTokenCreated = async (tokenString: string) => {
		setNewlyCreatedToken(tokenString);
		setIsCreateDialogOpen(false);
		await refetch();
	};

	return (
		<div className="space-y-6">
			<Card className="w-[95%] mx-auto">
				<CardHeader className="flex flex-row items-start justify-between">
					<div>
						<CardTitle>API Tokens</CardTitle>
						<CardDescription>
							Manage authentication tokens for accessing the Loco API
						</CardDescription>
					</div>
					<Button
						onClick={() => {
							setIsCreateDialogOpen(true);
						}}
						size="sm"
						className="h-8 px-3 text-sm bg-black dark:bg-white text-white dark:text-black hover:bg-black/90 dark:hover:bg-white/90 leading-relaxed"
					>
						<Plus className="h-4 w-4 mr-2" />
						Create Token
					</Button>
				</CardHeader>
				<CardContent>
					{/* Warning Banner */}
					<div className="bg-blue-50 border border-blue-200 rounded-lg p-4 flex gap-4 mb-6">
						<AlertCircle className="w-5 h-5 text-blue-600 shrink-0 mt-0.5" />
						<div>
							<h3 className="font-semibold text-blue-900 text-sm mb-1">
								Keep your tokens secure
							</h3>
							<p className="text-sm text-blue-800">
								API tokens provide full access to your account. Never share them
								publicly or commit them to version control.
							</p>
						</div>
					</div>

					{/* Tokens List or Loading/Empty State */}
					{isLoading ? (
						<div className="flex items-center justify-center py-12">
							<div className="text-center">
								<Loader2 className="w-8 h-8 animate-spin mx-auto mb-2 text-gray-400" />
								<p className="text-gray-600">Loading tokens...</p>
							</div>
						</div>
					) : tokens.length === 0 ? (
						<div className="flex items-center justify-center py-12">
							<div className="text-center">
								<p className="text-gray-600">
									No tokens yet. Create one to get started.
								</p>
							</div>
						</div>
					) : (
						<div className="space-y-4">
							{tokens.map((token) => (
								<TokenCard
									key={token.name}
									token={token}
									onRevokeToken={handleRevokeToken}
									isRevoking={isRevoking}
								/>
							))}
						</div>
					)}
				</CardContent>
			</Card>

			{/* Create Token Dialog */}
			<CreateTokenDialog
				open={isCreateDialogOpen}
				onOpenChange={setIsCreateDialogOpen}
				activeOrgId={activeOrgId}
				onSuccess={(tokenString) => {
					void handleTokenCreated(tokenString);
				}}
				tokens={tokens}
			/>

			{/* Token Display Dialog */}
			<TokenDisplayDialog
				open={!!newlyCreatedToken}
				onOpenChange={(open) => {
					if (!open) setNewlyCreatedToken(null);
				}}
				token={newlyCreatedToken ?? ""}
			/>
		</div>
	);
}
