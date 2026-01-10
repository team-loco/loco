import { useState, useMemo } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { createToken } from "@/gen/token/v1";
import { EntityType, Scope, EntityScopeSchema } from "@/gen/token/v1/token_pb";
import { listUserOrgs } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { listWorkspaceResources } from "@/gen/resource/v1";
import { useAuth } from "@/auth/AuthProvider";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Badge } from "@/components/ui/badge";
import {
	Loader,
	Plus,
	X,
	Shield,
	ShieldCheck,
	ShieldAlert,
	ChevronRight,
	Box,
} from "lucide-react";
import {
	Collapsible,
	CollapsibleContent,
	CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import { create } from "@bufbuild/protobuf";

interface CreateTokenDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	activeOrgId: bigint | null;
	onSuccess: (tokenString: string) => void;
}

interface ScopeSelection {
	entityType: EntityType;
	entityId: bigint;
	entityName?: string;
	scope: Scope;
}

const SCOPE_OPTIONS = [
	{
		value: Scope.READ,
		label: "Read",
		icon: Shield,
		description: "View resources and data",
		color: "text-gray-600",
	},
	{
		value: Scope.WRITE,
		label: "Write",
		icon: ShieldCheck,
		description: "Create and modify resources",
		color: "text-blue-600",
	},
	{
		value: Scope.ADMIN,
		label: "Admin",
		icon: ShieldAlert,
		description: "Full administrative access",
		color: "text-red-600",
	},
];

const EXPIRATION_OPTIONS = [
	{ label: "7 days", value: 7 * 24 * 60 * 60 },
	{ label: "14 days", value: 14 * 24 * 60 * 60 },
	{ label: "30 days", value: 30 * 24 * 60 * 60 },
	{ label: "60 days", value: 60 * 24 * 60 * 60 },
	{ label: "90 days", value: 90 * 24 * 60 * 60 },
];

interface WorkspaceTreeItemProps {
	workspace: { id: bigint; name: string };
	isExpanded: boolean;
	onToggleExpand: () => void;
	onScopeSelect: (
		entityType: EntityType,
		entityId: bigint,
		scope: Scope,
		entityName?: string
	) => void;
	scopeOptions: typeof SCOPE_OPTIONS;
}

function WorkspaceTreeItem({
	workspace,
	isExpanded,
	onToggleExpand,
	onScopeSelect,
	scopeOptions,
}: WorkspaceTreeItemProps) {
	// Fetch resources only when expanded
	const { data: resourcesRes } = useQuery(
		listWorkspaceResources,
		{ workspaceId: workspace.id },
		{ enabled: isExpanded }
	);

	const resources = useMemo(
		() => resourcesRes?.resources ?? [],
		[resourcesRes]
	);

	return (
		<Collapsible open={isExpanded} onOpenChange={onToggleExpand}>
			<div className="space-y-1">
				{/* Workspace header row */}
				<div className="flex gap-1.5 items-center">
					<Tooltip>
						<CollapsibleTrigger asChild>
							<TooltipTrigger asChild>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="h-7 w-7 p-0"
								>
									<ChevronRight
										className={cn(
											"h-4 w-4 transition-transform duration-200",
											isExpanded && "rotate-90"
										)}
									/>
								</Button>
							</TooltipTrigger>
						</CollapsibleTrigger>
						<TooltipContent side="left">
							<p className="text-xs">
								{isExpanded ? "Hide resources" : "Show resources"}
							</p>
						</TooltipContent>
					</Tooltip>
					<span className="text-sm flex-1 truncate">{workspace.name}</span>
					{scopeOptions.map((scopeOption) => {
						const Icon = scopeOption.icon;
						return (
							<Button
								key={scopeOption.value}
								type="button"
								variant="outline"
								size="sm"
								onClick={() =>
									onScopeSelect(
										EntityType.WORKSPACE,
										workspace.id,
										scopeOption.value,
										workspace.name
									)
								}
								title={scopeOption.description}
								className="h-7 w-7 p-0"
							>
								<Icon className={`h-3.5 w-3.5 ${scopeOption.color}`} />
							</Button>
						);
					})}
				</div>

				{/* Resources content */}
				<CollapsibleContent>
					<div className="pl-8 space-y-1 border-l-2 border-border ml-3 mt-1">
						{resources.length > 0 ? (
							resources.map((resource) => (
								<div
									key={resource.id.toString()}
									className="flex gap-1.5 items-center py-0.5"
								>
									<Box className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
									<span className="text-sm flex-1 truncate text-muted-foreground">
										{resource.name}
									</span>
									{scopeOptions.map((scopeOption) => {
										const Icon = scopeOption.icon;
										return (
											<Button
												key={scopeOption.value}
												type="button"
												variant="outline"
												size="sm"
												onClick={() =>
													onScopeSelect(
														EntityType.RESOURCE,
														resource.id,
														scopeOption.value,
														resource.name
													)
												}
												title={scopeOption.description}
												className="h-7 w-7 p-0"
											>
												<Icon className={`h-3.5 w-3.5 ${scopeOption.color}`} />
											</Button>
										);
									})}
								</div>
							))
						) : (
							<p className="text-xs text-muted-foreground italic py-1">
								No resources in this workspace
							</p>
						)}
					</div>
				</CollapsibleContent>
			</div>
		</Collapsible>
	);
}

export function CreateTokenDialog({
	open,
	onOpenChange,
	activeOrgId,
	onSuccess,
}: CreateTokenDialogProps) {
	const { user } = useAuth();

	// Tokens are created for the user entity
	// The activeOrgId provides context for which org's resources the user can grant access to
	const entityType = EntityType.USER;
	const entityId = user?.id ?? 0n;
	const [tokenName, setTokenName] = useState("");
	const [expiresInSec, setExpiresInSec] = useState(30 * 24 * 60 * 60); // 30 days default
	const [selectedScopes, setSelectedScopes] = useState<ScopeSelection[]>([]);

	// Fetch the active org and its workspaces for scope selection
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? 0n },
		{ enabled: !!user && open }
	);
	const activeOrg = useMemo(() => {
		const allOrgs = orgsRes?.orgs ?? [];
		return allOrgs.find((org) => org.id === activeOrgId);
	}, [orgsRes, activeOrgId]);

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		activeOrgId ? { orgId: activeOrgId } : undefined,
		{ enabled: !!activeOrgId && open }
	);
	const workspaces = useMemo(
		() => workspacesRes?.workspaces ?? [],
		[workspacesRes]
	);

	// Track which workspaces are expanded to show resources
	const [expandedWorkspaceIds, setExpandedWorkspaceIds] = useState<Set<string>>(
		new Set()
	);

	const { mutate: createTokenMutation, isPending } = useMutation(createToken);

	// Toggle workspace expansion
	const toggleWorkspaceExpansion = (workspaceId: bigint) => {
		setExpandedWorkspaceIds((prev) => {
			const next = new Set(prev);
			const key = workspaceId.toString();
			if (next.has(key)) {
				next.delete(key);
			} else {
				next.add(key);
			}
			return next;
		});
	};

	// Add a scope selection
	const addScopeSelection = (
		scopeEntityType: EntityType,
		scopeEntityId: bigint,
		scope: Scope,
		entityName?: string
	) => {
		const newScope: ScopeSelection = {
			entityType: scopeEntityType,
			entityId: scopeEntityId,
			entityName,
			scope,
		};

		// Check if this exact scope already exists
		const exists = selectedScopes.some(
			(s) =>
				s.entityType === scopeEntityType &&
				s.entityId === scopeEntityId &&
				s.scope === scope
		);

		if (!exists) {
			setSelectedScopes([...selectedScopes, newScope]);
		}

		// Auto-expand workspace when workspace scope is selected
		if (scopeEntityType === EntityType.WORKSPACE) {
			setExpandedWorkspaceIds((prev) => {
				const next = new Set(prev);
				next.add(scopeEntityId.toString());
				return next;
			});
		}
	};

	// Get entity name for display
	const getEntityName = (eType: EntityType, eId: bigint): string => {
		if (eType === EntityType.USER && eId === user?.id) {
			return user?.name || user?.email || "User";
		}
		if (eType === EntityType.ORGANIZATION) {
			return activeOrg?.name ?? `Organization ${eId}`;
		}
		if (eType === EntityType.WORKSPACE) {
			const ws = workspaces.find((w) => w.id === eId);
			return ws?.name ?? `Workspace ${eId}`;
		}
		if (eType === EntityType.RESOURCE) {
			// Resource names are passed via entityName parameter when scope is selected
			return `Resource ${eId}`;
		}
		return `Entity ${eId}`;
	};

	// Get entity type display name
	const getEntityTypeDisplay = (eType: EntityType): string => {
		const map: Record<number, string> = {
			[EntityType.USER]: "User",
			[EntityType.ORGANIZATION]: "Organization",
			[EntityType.WORKSPACE]: "Workspace",
			[EntityType.RESOURCE]: "Resource",
			[EntityType.SYSTEM]: "System",
		};
		return map[eType] || "Unknown";
	};

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();

		if (!tokenName.trim()) {
			toast.error("Token name is required");
			return;
		}

		if (selectedScopes.length === 0) {
			toast.error("At least one scope is required");
			return;
		}

		const protoScopes = selectedScopes.map((s) =>
			create(EntityScopeSchema, {
				entityType: s.entityType,
				entityId: s.entityId,
				scope: s.scope,
			})
		);

		createTokenMutation(
			{
				name: tokenName.trim(),
				entityType,
				entityId,
				scopes: protoScopes,
				expiresInSec: BigInt(expiresInSec),
			},
			{
				onSuccess: (response) => {
					if (response.token) {
						toast.success("Token created successfully");
						onSuccess(response.token);
						handleClose();
					}
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Failed to create token"));
				},
			}
		);
	};

	const handleClose = () => {
		setTokenName("");
		setExpiresInSec(30 * 24 * 60 * 60);
		setSelectedScopes([]);
		setExpandedWorkspaceIds(new Set());
		onOpenChange(false);
	};

	return (
		<Dialog open={open} onOpenChange={handleClose}>
			<DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
				<form onSubmit={handleSubmit}>
					<DialogHeader>
						<DialogTitle>Create API Token</DialogTitle>
						<DialogDescription>
							Create a new token for {getEntityTypeDisplay(entityType)}:{" "}
							{getEntityName(entityType, entityId)}
						</DialogDescription>
					</DialogHeader>

					<div className="grid gap-4 py-4">
						{/* Token Name and Expiration */}
						<div className="flex gap-3">
							<div className="space-y-1.5 flex-1">
								<Label htmlFor="token-name" className="text-sm font-medium">
									Token Name <span className="text-destructive">*</span>
								</Label>
								<Input
									id="token-name"
									placeholder="e.g., CI/CD Pipeline"
									value={tokenName}
									onChange={(e) => setTokenName(e.target.value)}
									className="border-border"
									autoFocus
								/>
							</div>

							<div className="space-y-1.5 ml-auto">
								<Label htmlFor="expiration" className="text-sm font-medium">
									Expiration <span className="text-destructive">*</span>
								</Label>
								<Select
									value={expiresInSec.toString()}
									onValueChange={(value) =>
										setExpiresInSec(parseInt(value, 10))
									}
								>
									<SelectTrigger id="expiration" className="border-border">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{EXPIRATION_OPTIONS.map((option) => (
											<SelectItem
												key={option.value}
												value={option.value.toString()}
											>
												{option.label}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						</div>

						<Separator className="my-1" />

						{/* Scopes Section */}
						<div className="space-y-3">
							<div>
								<Label className="text-sm font-medium">
									Permissions <span className="text-destructive">*</span>
								</Label>
								<p className="text-xs text-muted-foreground mt-0.5">
									Select entities and permission levels
								</p>
							</div>

							{/* Permissions Selection */}
							<div className="space-y-2.5 p-3 border border-border rounded-lg">
								{/* Self Permissions */}
								<div className="space-y-1.5">
									<Label className="text-xs text-muted-foreground">
										Account
									</Label>
									<div className="flex gap-1.5">
										<span className="text-sm flex-1 truncate">
											{getEntityName(entityType, entityId)}
										</span>
										{SCOPE_OPTIONS.map((scopeOption) => {
											const Icon = scopeOption.icon;
											return (
												<Button
													key={scopeOption.value}
													type="button"
													variant="outline"
													size="sm"
													onClick={() =>
														addScopeSelection(
															entityType,
															entityId,
															scopeOption.value,
															getEntityName(entityType, entityId)
														)
													}
													title={scopeOption.description}
													className="h-7 w-7 p-0"
												>
													<Icon
														className={`h-3.5 w-3.5 ${scopeOption.color}`}
													/>
												</Button>
											);
										})}
									</div>
								</div>

								{/* Organization Scopes */}
								{activeOrg && (
									<div className="space-y-1.5">
										<Label className="text-xs text-muted-foreground">
											Organization
										</Label>
										<div className="flex gap-1.5">
											<span className="text-sm flex-1 truncate">
												{activeOrg.name}
											</span>
											{SCOPE_OPTIONS.map((scopeOption) => {
												const Icon = scopeOption.icon;
												return (
													<Button
														key={scopeOption.value}
														type="button"
														variant="outline"
														size="sm"
														onClick={() =>
															addScopeSelection(
																EntityType.ORGANIZATION,
																activeOrg.id,
																scopeOption.value,
																activeOrg.name
															)
														}
														title={scopeOption.description}
														className="h-7 w-7 p-0"
													>
														<Icon
															className={`h-3.5 w-3.5 ${scopeOption.color}`}
														/>
													</Button>
												);
											})}
										</div>
									</div>
								)}

								{/* Workspaces & Resources */}
								{activeOrgId && workspaces.length > 0 && (
									<div className="space-y-1.5">
										<Label className="text-xs text-muted-foreground">
											Workspaces & Resources
										</Label>
										<div className="space-y-1">
											{workspaces.map((workspace) => (
												<WorkspaceTreeItem
													key={workspace.id.toString()}
													workspace={workspace}
													isExpanded={expandedWorkspaceIds.has(
														workspace.id.toString()
													)}
													onToggleExpand={() =>
														toggleWorkspaceExpansion(workspace.id)
													}
													onScopeSelect={addScopeSelection}
													scopeOptions={SCOPE_OPTIONS}
												/>
											))}
										</div>
									</div>
								)}
							</div>

							{/* Selected Scopes List */}
							<div className="space-y-1.5">
								<Label className="text-xs">
									Selected{" "}
									{selectedScopes.length > 0 && (
										<>
											(
											{(() => {
												// Group by entity to count unique entities
												const entities = new Map<string, Set<number>>();
												selectedScopes.forEach((scope) => {
													const key = `${scope.entityType}-${scope.entityId}`;
													if (!entities.has(key)) {
														entities.set(key, new Set());
													}
													entities.get(key)!.add(scope.scope);
												});
												return entities.size;
											})()}
											)
										</>
									)}
								</Label>
								<div className="flex flex-wrap gap-1 max-h-32 overflow-y-auto p-2 border border-border rounded-md bg-muted/20 min-h-12">
									{selectedScopes.length === 0 ? (
										<span className="text-xs text-muted-foreground">
											No permissions selected. Click the icons above to add
											permissions.
										</span>
									) : (
										(() => {
											// Group scopes by entity
											const entityGroups = new Map<
												string,
												{
													entityType: EntityType;
													entityId: bigint;
													entityName: string | undefined;
													scopes: Scope[];
													indices: number[];
												}
											>();

											selectedScopes.forEach((scope, index) => {
												const key = `${scope.entityType}-${scope.entityId}`;
												if (!entityGroups.has(key)) {
													entityGroups.set(key, {
														entityType: scope.entityType,
														entityId: scope.entityId,
														entityName: scope.entityName,
														scopes: [],
														indices: [],
													});
												}
												const group = entityGroups.get(key)!;
												if (!group.scopes.includes(scope.scope)) {
													group.scopes.push(scope.scope);
												}
												group.indices.push(index);
											});

											// Map entity type to badge variant
											const entityVariantMap: Record<
												EntityType,
												| "neo-blue"
												| "neo-purple"
												| "neo-green"
												| "neo-orange"
												| "neo-red"
												| "neo-gray"
											> = {
												[EntityType.USER]: "neo-blue",
												[EntityType.ORGANIZATION]: "neo-purple",
												[EntityType.WORKSPACE]: "neo-green",
												[EntityType.RESOURCE]: "neo-orange",
												[EntityType.SYSTEM]: "neo-red",
												[EntityType.UNSPECIFIED]: "neo-gray",
											};

											// Get short scope label
											const scopeShortMap: Record<Scope, string> = {
												[Scope.READ]: "R",
												[Scope.WRITE]: "W",
												[Scope.ADMIN]: "A",
												[Scope.UNSPECIFIED]: "?",
											};

											return Array.from(entityGroups.entries()).map(
												([key, group]) => {
													const scopeStr = group.scopes
														.sort()
														.map((s) => scopeShortMap[s])
														.join("");
													const entityTypeLabel = getEntityTypeDisplay(
														group.entityType
													);

													return (
														<div
															key={key}
															className="inline-flex items-center gap-1"
														>
															<Badge
																variant={entityVariantMap[group.entityType]}
																className="h-5 text-[10px] px-1.5 py-0"
															>
																{entityTypeLabel}:{" "}
																{group.entityName ||
																	getEntityName(
																		group.entityType,
																		group.entityId
																	)}
																: {scopeStr}
															</Badge>
															<Button
																type="button"
																variant="ghost"
																size="sm"
																onClick={() => {
																	// Remove all scopes for this entity
																	const newScopes = selectedScopes.filter(
																		(_, idx) => !group.indices.includes(idx)
																	);
																	setSelectedScopes(newScopes);
																}}
																className="h-5 w-5 p-0 hover:bg-destructive/10"
															>
																<X className="h-3 w-3" />
															</Button>
														</div>
													);
												}
											);
										})()
									)}
								</div>
							</div>
						</div>
					</div>

					<DialogFooter>
						<Button
							type="button"
							variant="secondary"
							onClick={handleClose}
							disabled={isPending}
						>
							Cancel
						</Button>
						<Button
							type="submit"
							disabled={
								isPending || !tokenName.trim() || selectedScopes.length === 0
							}
							title={
								!tokenName.trim()
									? "Token name is required"
									: selectedScopes.length === 0
									? "At least one scope is required"
									: undefined
							}
							className={
								!tokenName.trim() || selectedScopes.length === 0
									? "opacity-50 cursor-not-allowed"
									: ""
							}
						>
							{isPending ? (
								<>
									<Loader className="h-4 w-4 animate-spin mr-2" />
									Creating...
								</>
							) : (
								<>
									<Plus className="h-4 w-4 mr-2" />
									Create Token
								</>
							)}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
