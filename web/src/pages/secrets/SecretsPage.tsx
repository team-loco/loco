import { createConnectQueryKey, useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { deleteSecrets, listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import type { Secret } from "@gen/loco/secret/v1/secret_pb";
import { useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";
import { CopyIcon, PencilIcon, PlusIcon, Trash2Icon, UploadIcon } from "lucide-react";
import { parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "@/lib/toast";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { DocsButton } from "@/components/design/DocsButton";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { EmptyHero } from "@/components/design/EmptyHero";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { RowMenu } from "@/components/design/RowMenu";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { workspaceMembersOptions } from "@/hooks/completeMembers";
import { useCopy } from "@/hooks/useCopy";
import { useNow } from "@/hooks/useNow";
import { toastConnectError } from "@/lib/error-handler";
import { resourcePath } from "@/lib/routes";
import { daysAgoLabel, formatStarted, tsMs } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { referenceOf } from "@/pages/resource/variables";
import { DOCS_URL } from "@/pages/splash/links";

import { ImportDialog, SecretDialog } from "./SecretDialogs";
import { SecretsArt } from "./SecretsArt";
import { deleteBlocked, deleteBody, matchesSearch, setterLabel } from "./secrets";

const SECRETS_DOCS = `${DOCS_URL}/operations/variables/`;
const PAGE_PARAMS = { new: parseAsStringLiteral(["1"]), q: parseAsString.withDefault("") };

type Modal = { kind: "edit"; name: string | null } | { kind: "import" } | { kind: "delete"; secret: Secret };

export function SecretsPage({
	orgId,
	workspaceId,
	environmentId,
	environmentName,
	environmentSlug,
	canWrite,
}: {
	orgId: string;
	workspaceId: string;
	environmentId: string;
	environmentName: string;
	environmentSlug: string;
	canWrite: boolean;
}) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const timePref = useTimePreferences();
	const now = useNow();
	const [, copy] = useCopy();
	const query = useQuery(listSecrets, { environmentId });
	const { data: membersRes } = useTanstackQuery(workspaceMembersOptions(transport, workspaceId));
	const remove = useMutation(deleteSecrets);
	const [{ new: linkedNew, q: search }, setParams] = useQueryStates(PAGE_PARAMS, { history: "replace" });
	const [modal, setModal] = useState<Modal | null>(null);

	const secrets = query.data?.secrets ?? [];
	const shown = secrets.filter((secret) => matchesSearch(secret, search));
	const names = new Set(secrets.map((secret) => secret.name));
	const members = new Map((membersRes?.members ?? []).map((m) => [m.userId, m.userName || m.userEmail]));
	const listKey = createConnectQueryKey({ schema: listSecrets, input: { environmentId }, cardinality: "finite" });
	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: listKey });
	};
	const editing = modal?.kind === "edit" ? modal : linkedNew === "1" && canWrite ? { name: null } : null;
	const closeEdit = () => {
		setModal(null);
		void setParams({ new: null });
	};
	const openNew = () => {
		setModal({ kind: "edit", name: null });
	};
	const table = useDataTable("secrets", {
		columns: secretColumns({
			orgId,
			workspaceId,
			environmentSlug,
			canWrite,
			members,
			timePref,
			now,
			onUpdate: (name) => {
				setModal({ kind: "edit", name });
			},
			onCopy: (name) => {
				const ref = referenceOf("secrets", name);
				copy("ref", ref, `Copied ${ref}`);
			},
			onDelete: (secret) => {
				setModal({ kind: "delete", secret });
			},
		}),
		data: shown,
		getRowId: (secret) => secret.name,
	});
	const users = (name: string | null) =>
		(secrets.find((secret) => secret.name === name)?.usedBy ?? []).map((user) => user.resourceSlug);

	return (
		<Page className="gap-[22px]">
			<PageHeader
				title="Secrets"
				actions={
					canWrite && (
						<div className="flex gap-2">
							<Button
								variant="outline"
								onClick={() => {
									setModal({ kind: "import" });
								}}
							>
								<UploadIcon />
								Import .env
							</Button>
							<Button onClick={openNew}>
								<PlusIcon />
								New secret
							</Button>
						</div>
					)
				}
			/>
			{query.error !== null ? (
				<ErrorState error={query.error} what="secrets" rpc={listSecrets} onRetry={query.refetch} />
			) : (
				<Section
					filters={
						<span className="min-w-0 flex-[1_1_320px] text-[12.5px] leading-[22px] text-fg3">
							Scoped to{" "}
							<span className="mx-0.5 inline-block rounded-[4px] bg-panel2 px-1.5 font-mono text-[12px] leading-[18px] text-foreground">
								{environmentName}
							</span>
							. Reference one with{" "}
							<code className="mx-0.5 inline-block rounded-[4px] whitespace-nowrap bg-panel2 px-1.5 font-mono text-[12px] leading-[18px] text-foreground">
								{referenceOf("secrets", "NAME")}
							</code>
							.
						</span>
					}
					search={
						<SearchInput
							aria-label="Search secrets"
							placeholder="Search by name or resource"
							value={search}
							onClear={() => {
								void setParams({ q: null });
							}}
							onChange={(e) => {
								void setParams({ q: e.target.value });
							}}
						/>
					}
				>
					{query.isPending ? (
						<div className="flex flex-col gap-2 px-5 py-3">
							<Skeleton className="h-9 w-full" />
							<Skeleton className="h-9 w-full" />
						</div>
					) : (
						<DataTable
							table={table}
							label="Secrets"
							band
							empty={
								secrets.length === 0 ? (
									<EmptyHero
										art={<SecretsArt environment={environmentName} />}
										title={`No secrets in ${environmentName}`}
										subtitle={`Secrets belong to ${environmentName}. Any resource here can refer to one from its variables.`}
										className="rounded-none"
									>
										<div className="mt-1.5 flex flex-wrap justify-center gap-2">
											{canWrite && (
												<Button onClick={openNew}>
													<PlusIcon />
													New secret
												</Button>
											)}
											<DocsButton href={SECRETS_DOCS} />
										</div>
									</EmptyHero>
								) : (
									<div className="px-5 py-7 text-center text-[13px] text-fg3">No secrets match.</div>
								)
							}
						/>
					)}
				</Section>
			)}
			<SecretDialog
				open={editing !== null}
				environmentId={environmentId}
				environmentName={environmentName}
				name={editing?.name ?? null}
				users={users(editing?.name ?? null)}
				existing={names}
				onClose={closeEdit}
				onSaved={() => {
					closeEdit();
					refresh();
				}}
			/>
			<ImportDialog
				open={modal?.kind === "import"}
				environmentId={environmentId}
				environmentName={environmentName}
				existing={names}
				onClose={() => {
					setModal(null);
				}}
				onSaved={() => {
					setModal(null);
					refresh();
				}}
			/>
			<ConfirmDialog
				open={modal?.kind === "delete"}
				title={modal?.kind === "delete" ? `Delete ${modal.secret.name}?` : ""}
				description={modal?.kind === "delete" ? deleteBody(modal.secret.name) : ""}
				confirmLabel="Delete"
				pending={remove.isPending}
				onClose={() => {
					setModal(null);
				}}
				onConfirm={() => {
					if (modal?.kind !== "delete") return;
					const name = modal.secret.name;
					remove.mutate(
						{ environmentId, names: [name] },
						{
							onSuccess: () => {
								toast.success(`Deleted ${name}`);
								setModal(null);
								refresh();
							},
							onError: (err) => {
								toastConnectError(err, `Couldn't delete ${name}`);
							},
						},
					);
				}}
			/>
		</Page>
	);
}

function secretColumns({
	orgId,
	workspaceId,
	environmentSlug,
	canWrite,
	members,
	timePref,
	now,
	onUpdate,
	onCopy,
	onDelete,
}: {
	orgId: string;
	workspaceId: string;
	environmentSlug: string;
	canWrite: boolean;
	members: ReadonlyMap<string, string>;
	timePref: ReturnType<typeof useTimePreferences>;
	now: Date;
	onUpdate: (name: string) => void;
	onCopy: (name: string) => void;
	onDelete: (secret: Secret) => void;
}): DataColumn<Secret>[] {
	return [
		{
			id: "name",
			header: "Name",
			size: 220,
			minSize: 140,
			cell: ({ row: { original: secret } }) => (
				<span data-secret-name="" className="inline-block max-w-full font-mono text-[12.5px] leading-[18px] break-all">
					{secret.name}
				</span>
			),
		},
		{
			id: "usedBy",
			header: "Used by",
			size: 200,
			minSize: 120,
			meta: { grow: true },
			cell: ({ row: { original: secret } }) =>
				secret.usedBy.length === 0 ? (
					<span className="text-fg3">Not used</span>
				) : (
					<span className="flex min-w-0 flex-wrap gap-x-3 gap-y-1">
						{secret.usedBy.map((user) => (
							// The link uses its own hover group, so only the hovered link shows its arrow.
							<Link
								key={user.resourceId}
								className="group/used inline-flex items-center font-mono text-[13px] text-foreground no-underline"
								to={`${resourcePath(orgId, workspaceId, user.resourceId, { env: environmentSlug })}&tab=settings#set-env`}
							>
								{user.resourceSlug}
								<span
									aria-hidden="true"
									className="ml-1 text-fg3 opacity-0 transition-opacity group-hover/used:opacity-100 group-focus-visible/used:opacity-100"
								>
									→
								</span>
							</Link>
						))}
					</span>
				),
		},
		{
			id: "version",
			header: "Version",
			size: 90,
			minSize: 70,
			meta: { hideOnMobile: true, fit: true, cellClassName: "font-mono" },
			cell: ({ row: { original: secret } }) => (
				<CellText className="text-[12.5px] text-fg2" text={`v${secret.version.toString()}`} />
			),
		},
		{
			id: "updated",
			header: "Updated",
			size: 150,
			minSize: 110,
			meta: { hideOnMobile: true, fit: true },
			cell: ({ row: { original: secret } }) => (
				<span className="flex items-baseline gap-1.5 whitespace-nowrap">
					<Tooltip>
						<TooltipTrigger render={<span className="w-fit" />}>
							{daysAgoLabel(tsMs(secret.updatedAt), now.getTime())}
						</TooltipTrigger>
						<TooltipContent>{formatStarted(tsMs(secret.updatedAt), timePref)}</TooltipContent>
					</Tooltip>
					<span className="text-[12px] text-fg3">{setterLabel(secret, members)}</span>
				</span>
			),
		},
		{
			id: "menu",
			meta: { pinEnd: true, label: "Actions" },
			cell: ({ row: { original: secret } }) => (
				<RowMenu label={`Actions for ${secret.name}`} title={<span className="font-mono">{secret.name}</span>}>
					{canWrite && (
						<DropdownMenuItem
							onClick={() => {
								onUpdate(secret.name);
							}}
						>
							<PencilIcon />
							Update value
						</DropdownMenuItem>
					)}
					<DropdownMenuItem
						onClick={() => {
							onCopy(secret.name);
						}}
					>
						<CopyIcon />
						Copy reference
					</DropdownMenuItem>
					{canWrite && (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuItem
								variant="destructive"
								disabled={secret.usedBy.length > 0}
								onClick={() => {
									onDelete(secret);
								}}
							>
								<Trash2Icon />
								<span className="flex flex-col">
									Delete
									{secret.usedBy.length > 0 && (
										<span className="text-[11.5px] text-fg3">
											{deleteBlocked(secret.usedBy.map((user) => user.resourceSlug))}
										</span>
									)}
								</span>
							</DropdownMenuItem>
						</>
					)}
				</RowMenu>
			),
		},
	];
}
