import { useState, useMemo, useCallback } from "react";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listTokens, revokeToken } from "@/gen/loco/token/v1";
import { EntityType } from "@/gen/loco/token/v1/token_pb";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { useAuth } from "@/auth/AuthProvider";
import { useOrgWorkspace } from "@/context/ContextProvider";
import {
	Card,
	CardContent,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { toastConnectError } from "@/lib/error-handler";
import { getTokenColumns } from "./tokens/columns";
import { DataTable } from "./tokens/data-table";
import { CreateTokenDialog } from "./tokens/CreateTokenDialog";
import { TokenDisplayDialog } from "./tokens/TokenDisplayDialog";

export function Tokens() {
	const queryClient = useQueryClient();
	const { user } = useAuth();

	// Dialog states
	const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
	const [newlyCreatedToken, setNewlyCreatedToken] = useState<string | null>(
		null
	);

	// Get active org from context
	const { activeOrgId } = useOrgWorkspace();

	// Fetch orgs to display org name in description
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		user?.id ? { userId: user.id } : undefined,
		{ enabled: !!user?.id }
	);
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);

	// Fetch tokens for the current user
	// TVM will filter based on what the user has access to within their org context
	const { data: tokensRes, isLoading } = useQuery(
		listTokens,
		user?.id ? { entityType: EntityType.USER, entityId: user.id } : undefined,
		{ enabled: !!user?.id }
	);
	const tokens = useMemo(() => tokensRes?.tokens ?? [], [tokensRes]);

	// Revoke token mutation
	const { mutate: revokeTokenMutation, isPending: isRevoking } = useMutation(
		revokeToken,
		{
			onSuccess: () => {
				toast.success("Token revoked successfully");
				queryClient.invalidateQueries({
					queryKey: [
						{
							service: "token.v1.TokenService",
							method: "ListTokens",
						},
					],
				});
			},
			onError: (error) => {
				toastConnectError(error, "Failed to revoke token");
			},
		}
	);

	// Handle token revocation
	const handleRevokeToken = useCallback(
		(tokenName: string, tokenEntityType: EntityType, tokenEntityId: bigint) => {
			revokeTokenMutation({
				name: tokenName,
				entityType: tokenEntityType,
				entityId: tokenEntityId,
			});
		},
		[revokeTokenMutation]
	);

	// Handle token creation success
	const handleTokenCreated = (tokenString: string) => {
		setNewlyCreatedToken(tokenString);
		setIsCreateDialogOpen(false);
		queryClient.invalidateQueries({
			queryKey: [
				{
					service: "token.v1.TokenService",
					method: "ListTokens",
				},
			],
		});
	};

	// Get columns for the table
	const columns = useMemo(
		() => getTokenColumns(handleRevokeToken, isRevoking),
		[handleRevokeToken, isRevoking]
	);

	const activeOrg = orgs.find((o) => o.id === activeOrgId);

	return (
		<div className="space-y-4">
			<div className="flex items-center justify-between">
				<div>
					<h1 className="text-3xl font-bold text-foreground">API Tokens</h1>
					<p className="text-xs text-muted-foreground mt-2 uppercase tracking-wide">
						{activeOrg
							? `Organization: ${activeOrg.name}`
							: "Manage API tokens for programmatic access"}
					</p>
				</div>
				<Button 
					onClick={() => setIsCreateDialogOpen(true)}
				>
					<Plus className="h-4 w-4 mr-2" />
					Create Token
				</Button>
			</div>

			{isLoading ? (
				<Card>
					<CardContent className="flex items-center justify-center py-12">
						<div className="text-center">
							<div className="flex flex-col gap-2 items-center">
								<div className="w-6 h-6 border-2 border-primary border-t-transparent rounded-full animate-spin"></div>
								<p className="text-foreground text-sm">Loading tokens...</p>
							</div>
						</div>
					</CardContent>
				</Card>
			) : tokens.length === 0 ? (
				<Card>
					<CardContent className="flex items-center justify-center py-12">
						<div className="text-center">
							<p className="text-muted-foreground mb-4">
								No tokens yet. Create one to get started.
							</p>
							<Button
								onClick={() => setIsCreateDialogOpen(true)}
								variant="outline"
							>
								<Plus className="h-4 w-4 mr-2" />
								Create Your First Token
							</Button>
						</div>
					</CardContent>
				</Card>
			) : (
				<DataTable columns={columns} data={tokens} isLoading={isLoading} />
			)}

			{/* Create Token Dialog */}
			<CreateTokenDialog
				open={isCreateDialogOpen}
				onOpenChange={setIsCreateDialogOpen}
				activeOrgId={activeOrgId}
				onSuccess={handleTokenCreated}
			/>

			{/* Token Display Dialog (shows newly created token) */}
			<TokenDisplayDialog
				open={!!newlyCreatedToken}
				onOpenChange={(open) => !open && setNewlyCreatedToken(null)}
				token={newlyCreatedToken || ""}
			/>
		</div>
	);
}
