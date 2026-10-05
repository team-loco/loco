import { create } from "@bufbuild/protobuf";
import { createConnectQueryKey, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronDownIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { EnvironmentSchema, EnvironmentType, type Environment } from "@gen/loco/environment/v1/environment_pb";
import {
	createEnvironment,
	deleteEnvironment,
	listEnvironments,
} from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";

import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Input } from "@/components/design/Input";
import { environmentDotClass, environmentTypeLabel } from "@/hooks/useEnvironment";
import { toastConnectError } from "@/lib/error-handler";
import { cn } from "@/lib/utils";

const TIERS = [EnvironmentType.STAGING, EnvironmentType.PRODUCTION];

export function EnvironmentMenu({
	workspaceId,
	environments,
	active,
	onSelect,
	usedEnvIds,
}: {
	workspaceId: string;
	environments: Environment[];
	active: Environment | undefined;
	onSelect: (env: Environment) => void;
	usedEnvIds: Set<string>;
}) {
	const queryClient = useQueryClient();
	const [open, setOpen] = useState(false);
	const [creating, setCreating] = useState(false);
	const [name, setName] = useState("");
	const [tier, setTier] = useState(EnvironmentType.STAGING);

	const envsKey = createConnectQueryKey({
		schema: listEnvironments,
		input: { workspaceId },
		cardinality: "finite",
	});
	const createMutation = useMutation(createEnvironment);
	const deleteMutation = useMutation(deleteEnvironment);

	const trimmed = name.trim();
	const nameError =
		trimmed === ""
			? null
			: trimmed.length > 63
				? "Too long"
				: environments.some((e) => e.name === trimmed)
					? `${trimmed} already exists`
					: null;
	const canCreate = trimmed !== "" && nameError === null && !createMutation.isPending;

	const submit = async () => {
		if (!canCreate) return;
		try {
			const res = await createMutation.mutateAsync({ workspaceId, name: trimmed, type: tier });
			await queryClient.invalidateQueries({ queryKey: envsKey });
			const created = create(EnvironmentSchema, {
				id: res.environmentId,
				workspaceId,
				name: trimmed,
				type: tier,
			});
			onSelect(created);
			toast.success(`Environment ${trimmed} created`);
			setCreating(false);
			setOpen(false);
		} catch (err) {
			toastConnectError(err, "Failed to create environment");
		}
	};

	const remove = async (env: Environment) => {
		try {
			await deleteMutation.mutateAsync({ environmentId: env.id });
			await queryClient.invalidateQueries({ queryKey: envsKey });
			if (active?.id === env.id) {
				const next = environments.find((e) => e.id !== env.id);
				if (next !== undefined) onSelect(next);
			}
			toast.success(`Environment ${env.name} deleted`);
		} catch (err) {
			toastConnectError(err, "Failed to delete environment");
		}
	};

	const typeLabel = active !== undefined ? environmentTypeLabel(active.type) : "";
	const dot = active !== undefined ? environmentDotClass(active.type) : "bg-fg4";

	return (
		<DropdownMenu
			open={open}
			onOpenChange={(next: boolean) => {
				setOpen(next);
				if (!next) setCreating(false);
			}}
		>
			<DropdownMenuTrigger render={<Button variant="outline" className="ml-1 gap-2 px-2.5" />}>
				<span className={cn("size-[7px] rounded-full", dot)} />
				<span className="font-medium">{active?.name ?? "No environment"}</span>
				{typeLabel !== "" && <span className="text-sm text-fg3">{typeLabel}</span>}
				<ChevronDownIcon className="size-3 text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent className="w-auto min-w-[260px]">
				{environments.map((env) => {
					const used = usedEnvIds.has(env.id);
					return (
						<div
							key={env.id}
							className={cn("flex items-center rounded-sm", env.id === active?.id && "bg-bg3")}
						>
							<DropdownMenuItem
								className="flex-1 gap-2"
								onClick={() => {
									onSelect(env);
								}}
							>
								<span className={cn("size-[7px] rounded-full", environmentDotClass(env.type))} />
								<span className="flex-1">{env.name}</span>
								<span className="text-sm text-fg3">{environmentTypeLabel(env.type)}</span>
							</DropdownMenuItem>
							<span
								title={used ? "Environment has deployed resources" : "Delete environment"}
								className={cn("flex", used && "cursor-not-allowed")}
							>
								<Button
									variant="ghost"
									size="icon"
									aria-label={`Delete ${env.name}`}
									disabled={used || deleteMutation.isPending || environments.length <= 1}
									className="text-fg3 disabled:text-line2 disabled:opacity-100"
									onClick={(e) => {
										e.stopPropagation();
										void remove(env);
									}}
								>
									<Trash2Icon />
								</Button>
							</span>
						</div>
					);
				})}
				<DropdownMenuSeparator />
				{creating ? (
					<div
						className="flex flex-col gap-2 px-2.5 pt-2 pb-2.5"
						onKeyDown={(e) => {
							e.stopPropagation();
						}}
					>
						<Input
							autoFocus
							value={name}
							placeholder="Environment name"
							aria-invalid={nameError !== null}
							className="h-[30px]"
							onChange={(e) => {
								setName(e.target.value);
							}}
							onKeyDown={(e) => {
								if (e.key === "Enter") {
									e.preventDefault();
									void submit();
								}
								if (e.key === "Escape") setCreating(false);
							}}
						/>
						{nameError !== null && <span className="text-sm text-bad-fg">{nameError}</span>}
						<div className="flex gap-1">
							{TIERS.map((t) => (
								<Button
									key={t}
									variant="outline"
									size="xs"
									className={cn("flex-1", tier === t && "border-foreground bg-bg3 dark:border-foreground dark:bg-bg3")}
									onClick={() => {
										setTier(t);
									}}
								>
									{environmentTypeLabel(t)}
								</Button>
							))}
						</div>
						<div className="flex justify-end gap-1.5">
							<Button
								variant="outline"
								size="xs"
								onClick={() => {
									setCreating(false);
								}}
							>
								Cancel
							</Button>
							<Button
								size="xs"
								disabled={!canCreate}
								onClick={() => {
									void submit();
								}}
							>
								Create
							</Button>
						</div>
					</div>
				) : (
					<Button
						variant="ghost"
						className="h-8 w-full justify-start gap-2 px-2.5"
						onClick={() => {
							setName("");
							setTier(EnvironmentType.STAGING);
							setCreating(true);
						}}
					>
						<PlusIcon className="text-fg3" />
						New environment
					</Button>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
