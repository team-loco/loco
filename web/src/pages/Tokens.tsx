import { useState, useMemo, useCallback } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listTokens, revokeToken } from "@/gen/token/v1";
import { EntityType } from "@/gen/token/v1/token_pb";
import { listUserOrgs } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { useAuth } from "@/auth/AuthProvider";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Label } from "@/components/ui/label";
import { Plus, User, Building2, Briefcase } from "lucide-react";
import { toast } from "sonner";
import { toastConnectError } from "@/lib/error-handler";
import { getTokenColumns } from "./tokens/columns";
import { DataTable } from "./tokens/data-table";
import { CreateTokenDialog } from "./tokens/CreateTokenDialog";
import { TokenDisplayDialog } from "./tokens/TokenDisplayDialog";

export function Tokens() {
	const [searchParams, setSearchParams] = useSearchParams();
	const queryClient = useQueryClient();
	const { user } = useAuth();

	// Tab state - default to "personal"
	const activeTab = searchParams.get("tab") || "personal";
	const setActiveTab = (tab: string) => {
		setSearchParams({ tab });
	};

	// Entity selection state
	const [selectedOrgId, setSelectedOrgId] = useState<bigint | null>(null);
	const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<bigint | null>(
		null
	);

	// Dialog states
	const [isCreateDialogOpen, setIsCreateDialogOpen] = useState(false);
	const [newlyCreatedToken, setNewlyCreatedToken] = useState<string | null>(
		null
	);

	// Fetch user orgs and workspaces
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? 0n },
		{ enabled: !!user }
	);
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		selectedOrgId ? { orgId: selectedOrgId } : undefined,
		{ enabled: !!selectedOrgId }
	);
	const workspaces = useMemo(
		() => workspacesRes?.workspaces ?? [],
		[workspacesRes]
	);

	// Auto-select first org/workspace if none selected
	useMemo(() => {
		if (orgs.length > 0 && !selectedOrgId) {
			setSelectedOrgId(orgs[0].id);
		}
	}, [orgs, selectedOrgId]);

	useMemo(() => {
		if (workspaces.length > 0 && !selectedWorkspaceId) {
			setSelectedWorkspaceId(workspaces[0].id);
		}
	}, [workspaces, selectedWorkspaceId]);

	// Determine entity type and ID based on active tab
	const { entityType, entityId } = useMemo(() => {
		if (activeTab === "personal") {
			return {
				entityType: EntityType.USER,
				entityId: user?.id ?? 0n,
			};
		} else if (activeTab === "organization") {
			return {
				entityType: EntityType.ORGANIZATION,
				entityId: selectedOrgId ?? 0n,
			};
		} else if (activeTab === "workspace") {
			return {
				entityType: EntityType.WORKSPACE,
				entityId: selectedWorkspaceId ?? 0n,
			};
		}
		return { entityType: EntityType.UNSPECIFIED, entityId: 0n };
	}, [activeTab, user?.id, selectedOrgId, selectedWorkspaceId]);

	// Fetch tokens for the selected entity
	const { data: tokensRes, isLoading } = useQuery(
		listTokens,
		entityId && entityId !== 0n ? { entityType, entityId } : undefined,
		{ enabled: entityId !== 0n }
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
		(tokenName: string) => {
			if (!entityId) return;
			revokeTokenMutation({
				name: tokenName,
				entityType,
				entityId,
			});
		},
		[entityId, entityType, revokeTokenMutation]
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

	// Entity selector component
	const EntitySelector = () => {
		if (activeTab === "organization") {
			return (
				<div className="space-y-2">
					<Label htmlFor="org-select" className="text-sm font-medium">
						Organization
					</Label>
					<Select
						value={selectedOrgId?.toString() ?? ""}
						onValueChange={(value) => setSelectedOrgId(BigInt(value))}
					>
						<SelectTrigger id="org-select">
							<SelectValue placeholder="Select an organization" />
						</SelectTrigger>
						<SelectContent>
							{orgs.map((org) => (
								<SelectItem key={org.id.toString()} value={org.id.toString()}>
									{org.name}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
			);
		}

		if (activeTab === "workspace") {
			return (
				<div className="space-y-4">
					<div className="space-y-2">
						<Label htmlFor="org-select-ws" className="text-sm font-medium">
							Organization
						</Label>
						<Select
							value={selectedOrgId?.toString() ?? ""}
							onValueChange={(value) => {
								setSelectedOrgId(BigInt(value));
								setSelectedWorkspaceId(null); // Reset workspace selection
							}}
						>
							<SelectTrigger id="org-select-ws" className="border-border">
								<SelectValue placeholder="Select an organization" />
							</SelectTrigger>
							<SelectContent>
								{orgs.map((org) => (
									<SelectItem key={org.id.toString()} value={org.id.toString()}>
										{org.name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<div className="space-y-2">
						<Label htmlFor="workspace-select" className="text-sm font-medium">
							Workspace
						</Label>
						<Select
							value={selectedWorkspaceId?.toString() ?? ""}
							onValueChange={(value) => setSelectedWorkspaceId(BigInt(value))}
							disabled={!selectedOrgId || workspaces.length === 0}
						>
							<SelectTrigger id="workspace-select" className="border-border">
								<SelectValue placeholder="Select a workspace" />
							</SelectTrigger>
							<SelectContent>
								{workspaces.map((workspace) => (
									<SelectItem
										key={workspace.id.toString()}
										value={workspace.id.toString()}
									>
										{workspace.name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
				</div>
			);
		}

		return null;
	};

	return (
		<div className="space-y-6">
			<Card>
				<CardHeader>
					<CardTitle>API Tokens</CardTitle>
					<CardDescription>
						Create and manage API tokens for programmatic access
					</CardDescription>
				</CardHeader>
				<CardContent className="space-y-6">
					<Tabs value={activeTab} onValueChange={setActiveTab}>
						<TabsList className="grid w-full grid-cols-3">
							<TabsTrigger value="personal" className="flex items-center gap-2">
								<User className="h-4 w-4" />
								Personal Tokens
							</TabsTrigger>
							<TabsTrigger
								value="organization"
								className="flex items-center gap-2"
							>
								<Building2 className="h-4 w-4" />
								Organization Tokens
							</TabsTrigger>
							<TabsTrigger
								value="workspace"
								className="flex items-center gap-2"
							>
								<Briefcase className="h-4 w-4" />
								Workspace Tokens
							</TabsTrigger>
						</TabsList>

						<TabsContent value="personal" className="space-y-4 mt-6">
							<div className="flex justify-end">
								<Button onClick={() => setIsCreateDialogOpen(true)}>
									<Plus className="h-4 w-4 mr-2" />
									Create Personal Token
								</Button>
							</div>
							<DataTable
								columns={columns}
								data={tokens}
								isLoading={isLoading}
							/>
							{!isLoading && tokens.length === 0 && (
								<div className="text-center py-12">
									<p className="text-muted-foreground">
										No personal tokens yet. Create one to get started.
									</p>
								</div>
							)}
						</TabsContent>

						<TabsContent value="organization" className="space-y-4 mt-6">
							<EntitySelector />
							{selectedOrgId && (
								<>
									<div className="flex justify-end">
										<Button onClick={() => setIsCreateDialogOpen(true)}>
											<Plus className="h-4 w-4 mr-2" />
											Create Organization Token
										</Button>
									</div>
									<DataTable
										columns={columns}
										data={tokens}
										isLoading={isLoading}
									/>
									{!isLoading && tokens.length === 0 && (
										<div className="text-center py-12">
											<p className="text-muted-foreground">
												No organization tokens yet. Create one to get started.
											</p>
										</div>
									)}
								</>
							)}
						</TabsContent>

						<TabsContent value="workspace" className="space-y-4 mt-6">
							<EntitySelector />
							{selectedWorkspaceId && (
								<>
									<div className="flex justify-end">
										<Button onClick={() => setIsCreateDialogOpen(true)}>
											<Plus className="h-4 w-4 mr-2" />
											Create Workspace Token
										</Button>
									</div>
									<DataTable
										columns={columns}
										data={tokens}
										isLoading={isLoading}
									/>
									{!isLoading && tokens.length === 0 && (
										<div className="text-center py-12">
											<p className="text-muted-foreground">
												No workspace tokens yet. Create one to get started.
											</p>
										</div>
									)}
								</>
							)}
						</TabsContent>
					</Tabs>
				</CardContent>
			</Card>

			{/* Create Token Dialog */}
			<CreateTokenDialog
				open={isCreateDialogOpen}
				onOpenChange={setIsCreateDialogOpen}
				entityType={entityType}
				entityId={entityId}
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
