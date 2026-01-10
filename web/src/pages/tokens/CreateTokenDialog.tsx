import { useState, useMemo } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { createToken } from "@/gen/token/v1";
import { EntityType, Scope, EntityScopeSchema } from "@/gen/token/v1/token_pb";
import { listUserOrgs } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
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
} from "lucide-react";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/error-handler";
import { create } from "@bufbuild/protobuf";

interface CreateTokenDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	entityType: EntityType;
	entityId: bigint;
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

export function CreateTokenDialog({
	open,
	onOpenChange,
	entityType,
	entityId,
	onSuccess,
}: CreateTokenDialogProps) {
	const { user } = useAuth();
	const [tokenName, setTokenName] = useState("");
	const [expiresInSec, setExpiresInSec] = useState(30 * 24 * 60 * 60); // 30 days default
	const [selectedScopes, setSelectedScopes] = useState<ScopeSelection[]>([]);

	// Fetch orgs and workspaces for scope selection
	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? 0n },
		{ enabled: !!user && open }
	);
	const orgs = useMemo(() => orgsRes?.orgs ?? [], [orgsRes]);

	const [selectedOrgForScopes, setSelectedOrgForScopes] = useState<
		bigint | null
	>(null);
	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		selectedOrgForScopes ? { orgId: selectedOrgForScopes } : undefined,
		{ enabled: !!selectedOrgForScopes && open }
	);
	const workspaces = useMemo(
		() => workspacesRes?.workspaces ?? [],
		[workspacesRes]
	);

	const { mutate: createTokenMutation, isPending } = useMutation(createToken);

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
	};

	// Get entity name for display
	const getEntityName = (eType: EntityType, eId: bigint): string => {
		if (eType === EntityType.USER && eId === user?.id) {
			return user?.name || user?.email || "User";
		}
		if (eType === EntityType.ORGANIZATION) {
			const org = orgs.find((o) => o.id === eId);
			return org?.name ?? `Organization ${eId}`;
		}
		if (eType === EntityType.WORKSPACE) {
			const ws = workspaces.find((w) => w.id === eId);
			return ws?.name ?? `Workspace ${eId}`;
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
		setSelectedOrgForScopes(null);
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
										{getEntityTypeDisplay(entityType)}
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
								{orgs.length > 0 && (
									<div className="space-y-1.5">
										<Label className="text-xs text-muted-foreground">
											Organizations
										</Label>
										<div className="flex gap-1.5">
											<Select
												value={selectedOrgForScopes?.toString() ?? ""}
												onValueChange={(value) =>
													setSelectedOrgForScopes(BigInt(value))
												}
											>
												<SelectTrigger className="border-border flex-1 h-7 text-sm">
													<SelectValue placeholder="Select..." />
												</SelectTrigger>
												<SelectContent>
													{orgs.map((org) => (
														<SelectItem
															key={org.id.toString()}
															value={org.id.toString()}
														>
															{org.name}
														</SelectItem>
													))}
												</SelectContent>
											</Select>
											{selectedOrgForScopes && (
												<>
													{SCOPE_OPTIONS.map((scopeOption) => {
														const Icon = scopeOption.icon;
														return (
															<Button
																key={scopeOption.value}
																type="button"
																variant="outline"
																size="sm"
																onClick={() => {
																	const org = orgs.find(
																		(o) => o.id === selectedOrgForScopes
																	);
																	addScopeSelection(
																		EntityType.ORGANIZATION,
																		selectedOrgForScopes,
																		scopeOption.value,
																		org?.name
																	);
																}}
																title={scopeOption.description}
																className="h-7 w-7 p-0"
															>
																<Icon
																	className={`h-3.5 w-3.5 ${scopeOption.color}`}
																/>
															</Button>
														);
													})}
												</>
											)}
										</div>
									</div>
								)}

								{/* Workspace Scopes */}
								{selectedOrgForScopes && workspaces.length > 0 && (
									<div className="space-y-1.5">
										<Label className="text-xs text-muted-foreground">
											Workspaces
										</Label>
										{workspaces.map((workspace) => (
											<div
												key={workspace.id.toString()}
												className="flex gap-1.5 items-center"
											>
												<span className="text-sm flex-1 truncate">
													{workspace.name}
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
																	EntityType.WORKSPACE,
																	workspace.id,
																	scopeOption.value,
																	workspace.name
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
										))}
									</div>
								)}
							</div>

							{/* Selected Scopes List */}
							{selectedScopes.length > 0 && (
								<div className="space-y-1.5">
									<Label className="text-xs">
										Selected (
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
									</Label>
									<div className="flex flex-wrap gap-1 max-h-32 overflow-y-auto p-2 border border-border rounded-md bg-muted/20">
										{(() => {
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
															className="inline-flex items-center rounded-sm border-2 border-black dark:border-neutral-700 overflow-hidden"
														>
															<Badge
																variant={entityVariantMap[group.entityType]}
																className="h-5 text-[10px] px-1.5 py-0 rounded-none border-0 shadow-none"
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
																className="h-5 w-5 p-0 hover:bg-destructive/10 rounded-none border-l border-black dark:border-neutral-700"
															>
																<X className="h-3 w-3" />
															</Button>
														</div>
													);
												}
											);
										})()}
									</div>
								</div>
							)}
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
