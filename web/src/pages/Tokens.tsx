import { useState, useMemo, useCallback } from "react";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listTokens, revokeToken } from "@/gen/loco/token/v1";
import { EntityType } from "@/gen/loco/token/v1/token_pb";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { useAuth } from "@/auth/AuthProvider";
import { useOrgContext } from "@/hooks/useOrgContext";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
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

	// Get active org from global context
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? 0n },
		{ enabled: !!user }
	);
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);
	const orgIds = useMemo(() => orgs.map((o) => o.id), [orgs]);
	const { activeOrgId } = useOrgContext(orgIds);

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
		<div className="space-y-6">
			<Card>
				<CardHeader>
					<div className="flex items-start justify-between">
						<div>
							<CardTitle>API Tokens</CardTitle>
							<CardDescription>
								{activeOrg
									? `Manage API tokens for the organization: ${activeOrg.name}`
									: "Create and manage API tokens for programmatic access"}
							</CardDescription>
						</div>
						<Button onClick={() => setIsCreateDialogOpen(true)}>
							<Plus className="h-4 w-4 mr-2" />
							Create Token
						</Button>
					</div>
				</CardHeader>
				<CardContent className="space-y-6">
					<DataTable columns={columns} data={tokens} isLoading={isLoading} />
					{!isLoading && tokens.length === 0 && (
						<div className="text-center py-12">
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
					)}
				</CardContent>
			</Card>

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
