import { useQuery as useRpcQuery } from "@connectrpc/connect-query";
import { toast } from "@/lib/toast";
import { ChevronDownIcon, ChevronRightIcon, ClipboardPasteIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { cn } from "@/lib/utils";
import { useQueryState } from "nuqs";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { listAppDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import type { AppDomain } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { RegionCard, RegionCards } from "@/components/design/RegionCards";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/design/Collapsible";
import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { InlineInput } from "@/components/design/InlineInput";
import { Input } from "@/components/design/Input";
import { AppDomainInput } from "@/components/design/AppDomainInput";
import { SliderValue, StepSlider, stepLabel } from "@/components/design/StepSlider";
import { Notice } from "@/components/design/Notice";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { AutoscalingSwitch, autoscales, ScalingFields } from "@/components/design/ScalingFields";
import { Switch } from "@/components/design/Switch";
import { CommandFields, type CommandValue } from "@/components/design/CommandFields";
import { Spinner } from "@/components/design/Spinner";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useDomainAvailability } from "@/hooks/useDomainAvailability";
import { useImageCheck } from "@/hooks/useImageCheck";
import { useDrafts } from "@/hooks/useDrafts";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useTakenKeys, type TakenKeys } from "@/hooks/useTakenKeys";
import { DotEnvNotes } from "@/components/DotEnvNotes";
import { dotEnvNotes, parseDotEnv } from "@/lib/dotenv";
import { draftIsPublic, draftRegions, draftSizing, type Draft, type DraftEdits } from "@/lib/drafts";
import { getErrorMessage } from "@/lib/error-handler";
import { DRAFT_PARAM, resourcePath } from "@/lib/routes";
import {
	commandSummary,
	firstError,
	imageError,
	envRowErrors,
	locoYaml,
	mergeEnv,
	nameFromImage,
	servedDomains,
	validateForm,
	type ApiField,
	type EnvRow,
	type HealthForm,
	type NewResourceForm,
} from "@/lib/new-resource";
import { sizeOptions, type SizeOptions } from "@/lib/sizing";
import { derivedSlugError, nameError, slug as slugOf } from "@/lib/slug";
import { useServiceDeploy, type DeployError } from "@/hooks/useServiceDeploy";
import { DiscardDraftDialog } from "./DiscardDraftDialog";

const SKELETON_ROWS = 6;
const REGION_POLL_MS = 15_000;
const NON_DIGITS_RE = /\D/g;
const MAX_NAME_LENGTH = 63;
const NO_NAMES: ReadonlySet<string> = new Set();
const NETWORK_PUBLIC = "public";
const NETWORK_INTERNAL = "internal";

const API_FIELD_LABEL: Record<ApiField, string> = {
	image: "Container image",
	port: "Port",
	path: "Health check path",
	interval: "Health check interval",
	timeout: "Health check timeout",
	failAfter: "Health check fail after",
	cpu: "CPU",
	memory: "Memory",
	replicas: "Replicas",
	command: "Command",
	args: "Arguments",
};
const COMMAND_FIELDS: ReadonlySet<ApiField> = new Set(["command", "args"]);

export function NewService({
	orgId,
	workspaceId,
	env,
	loading,
	onClose,
	onCreated,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment | undefined;
	loading: boolean;
	onClose: () => void;
	onCreated: (draftId: string) => void;
}) {
	const store = useDrafts(orgId, workspaceId, env?.id);
	const [image, setImage] = useState("");
	const [editedName, setEditedName] = useState<string | null>(null);
	const taken = useTakenKeys(orgId, workspaceId, store.drafts);
	const name = editedName ?? nameFromImage(image, new Set([...taken.names, ...taken.slugs]));
	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<DialogContent title="New resource" titleClassName="text-[20px] font-normal tracking-[-0.02em]" size="form" top>
				<ImageStep
					orgId={orgId}
					workspaceId={workspaceId}
					env={env}
					loading={loading}
					image={image}
					name={name}
					taken={taken}
					onImage={setImage}
					onName={setEditedName}
					onContinue={() => {
						const id = crypto.randomUUID();
						const draft = {
							id,
							image: image.trim(),
							name: name.trim(),
							slug: slugOf(name),
							createdMs: Date.now(),
							edits: {},
						};
						if (store.create(draft)) onCreated(id);
						else toast.error("Couldn't save the draft in this browser");
					}}
				/>
			</DialogContent>
		</Dialog>
	);
}

function ImageStep({
	orgId,
	workspaceId,
	env,
	loading,
	image,
	name,
	taken,
	onImage,
	onName,
	onContinue,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment | undefined;
	loading: boolean;
	image: string;
	name: string;
	taken: TakenKeys;
	onImage: (image: string) => void;
	onName: (name: string) => void;
	onContinue: () => void;
}) {
	const scopes = useMyScopes();
	const [edited, setEdited] = useState(false);
	const error = imageError(image);
	const shown =
		edited && error !== null && image.trim() !== "" ? "Use registry/repository:tag or an @sha256 digest." : null;
	const nameErr = nameError(name, taken.names) ?? derivedSlugError(name, taken.slugs);

	const waiting = scopes.isLoading || loading;
	const ready = !waiting && error === null && nameErr === null;
	const notice = waiting
		? null
		: scopes.workspaceLevel(orgId, workspaceId) < scopeLevel(Scope.WRITE)
			? {
					title: "You can't create resources here",
					body: "Creating a resource needs write access to this workspace.",
				}
			: env === undefined
				? { title: "No environments yet", body: "Create an environment before adding a resource." }
				: null;
	if (notice !== null) {
		return (
			<>
				<DialogBody>
					<EmptyState title={notice.title}>{notice.body}</EmptyState>
				</DialogBody>
				<DialogFooter>
					<DialogClose render={<Button type="button" variant="outline" />}>Close</DialogClose>
				</DialogFooter>
			</>
		);
	}
	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				if (ready) onContinue();
			}}
		>
			<DialogBody>
				<Field label="Container image" hint="A public image. Private registries aren't supported yet." error={shown}>
					<Input
						autoFocus
						value={image}
						className="h-9 font-mono"
						placeholder="ghcr.io/acme/web:1.4.2"
						autoComplete="off"
						spellCheck={false}
						aria-invalid={shown !== null}
						onChange={(e) => {
							setEdited(true);
							onImage(e.target.value);
						}}
					/>
				</Field>
				{error === null && (
					<Field label="Name" error={nameErr}>
						<Input
							value={name}
							className="h-9"
							autoComplete="off"
							aria-invalid={nameErr !== null}
							onChange={(e) => {
								onName(e.target.value);
							}}
						/>
					</Field>
				)}
			</DialogBody>
			<DialogFooter>
				<DialogClose render={<Button type="button" variant="outline" />}>Cancel</DialogClose>
				<Button type="submit" disabled={!ready}>
					Continue
				</Button>
			</DialogFooter>
		</form>
	);
}

// The sheet stays mounted while closed so every open runs Base UI's starting-style transition;
// the panel inside is keyed by draft so each draft starts with fresh state.
export function DraftSheet({
	orgId,
	workspaceId,
	env,
	draftId,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	draftId: string | null;
}) {
	const [, setDraftParam] = useQueryState(DRAFT_PARAM);
	const open = draftId !== null;
	const heldId = useHeldWhileClosed(draftId, open);
	const close = () => {
		void setDraftParam(null);
	};
	return (
		<Sheet
			open={open}
			onOpenChange={(next) => {
				if (!next) close();
			}}
		>
			<SheetContent size="wide" className="data-[side=right]:w-[min(560px,calc(100vw-16px))]">
				{heldId !== null && (
					<DraftPanel
						key={heldId}
						orgId={orgId}
						workspaceId={workspaceId}
						env={env}
						draftId={heldId}
						open={open}
						close={close}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}

function DraftPanel({
	orgId,
	workspaceId,
	env,
	draftId,
	open,
	close,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	draftId: string;
	open: boolean;
	close: () => void;
}) {
	const store = useDrafts(orgId, workspaceId, env.id);
	const taken = useTakenKeys(orgId, workspaceId);
	const configQuery = useRpcQuery(getConfig, {});
	const regionsQuery = useRpcQuery(
		listRegions,
		{ environmentId: env.id },
		{ refetchInterval: open ? REGION_POLL_MS : false, refetchOnWindowFocus: open },
	);
	const domainsQuery = useRpcQuery(listAppDomains, { environmentId: env.id });
	const deployment = useServiceDeploy(workspaceId, env);
	const navigate = useNavigate();
	const [tried, setTried] = useState(false);
	const [commandOpen, setCommandOpen] = useState(false);
	const [discarding, setDiscarding] = useState(false);
	const [saveFailed, setSaveFailed] = useState(false);
	const [nameText, setNameText] = useState<string | null>(null);

	const draft = store.drafts.find((d) => d.id === draftId);
	const loadError = configQuery.error ?? regionsQuery.error ?? domainsQuery.error;
	const config = configQuery.data;
	const defaults = config?.serviceDefaults;
	const persist = (saved: boolean) => {
		if (!saved && !saveFailed) toast.error("Couldn't save changes to this draft");
		setSaveFailed(!saved);
	};
	const header = (
		<SheetHeader>
			<div className="flex min-w-0 flex-1 flex-col gap-1.5">
				<div className="flex min-w-0 items-center gap-2.5">
					{draft === undefined ? (
						<SheetTitle className="truncate text-[22px] font-normal tracking-[-0.02em]">Draft</SheetTitle>
					) : (
						<SheetTitle className="m-0 flex min-w-0 text-[22px] font-normal tracking-[-0.02em]">
							<InlineInput
								aria-label="Name"
								value={nameText ?? draft.name}
								size={Math.max(6, (nameText ?? draft.name).length + 1)}
								maxLength={MAX_NAME_LENGTH}
								autoComplete="off"
								spellCheck={false}
								className="-my-0.5 -ml-[5px] max-w-full rounded-[5px] px-[5px] py-px focus:shadow-[inset_0_0_0_1px_var(--line)]"
								onChange={(e) => {
									setNameText(e.target.value);
									if (
										nameError(e.target.value, NO_NAMES) === null &&
										derivedSlugError(e.target.value, NO_NAMES) === null
									) {
										persist(store.setName(draft.id, e.target.value));
									}
								}}
								onBlur={() => {
									setNameText(null);
									if (draft.name !== draft.name.trim()) persist(store.setName(draft.id, draft.name.trim()));
								}}
							/>
						</SheetTitle>
					)}
					<span className="flex shrink-0 items-center gap-1.5 text-[12.5px] text-fg2">
						<StatusGlyph glyph="draft" />
						Draft
					</span>
				</div>
				{draft !== undefined && (
					<InlineInput
						aria-label="Description"
						placeholder="Add a description"
						value={draft.edits.description ?? ""}
						autoComplete="off"
						wrapperClassName="flex-1"
						className="-mt-0.5 -ml-1 h-[22px] w-full rounded-[4px] px-1 text-[13.5px] text-fg2 placeholder:text-fg4 focus:text-foreground"
						onChange={(e) => {
							deployment.clearError();
							persist(store.edit(draft.id, { description: e.target.value }));
						}}
					/>
				)}
			</div>
		</SheetHeader>
	);
	const body = (() => {
		if (loadError !== null) {
			return (
				<SheetBody>
					<EmptyState title="Couldn't load the form">{getErrorMessage(loadError, "Failed to load")}</EmptyState>
				</SheetBody>
			);
		}
		if (config === undefined || defaults === undefined || regionsQuery.data === undefined) {
			return (
				<SheetBody role="status" aria-label="Loading draft">
					{Array.from({ length: SKELETON_ROWS }, (_, i) => (
						<Skeleton key={i} className="h-14 w-full" />
					))}
				</SheetBody>
			);
		}
		if (draft === undefined) {
			if (!open) return <SheetBody />;
			return (
				<SheetBody>
					<EmptyState title="This draft isn't here">
						It was deployed or discarded, or it was saved in another browser.
					</EmptyState>
				</SheetBody>
			);
		}
		return (
			<ServiceForm
				env={env}
				schemaUrl={config.schemaUrl}
				regions={regionsQuery.data.regions}
				defaultRegion={config.defaultRegion}
				appDomains={domainsQuery.data?.appDomains ?? []}
				draft={draft}
				defaults={defaults}
				sizes={sizeOptions(config.sizeLimits)}
				deployment={deployment}
				resourceHref={
					deployment.resourceId === undefined
						? undefined
						: resourcePath(orgId, workspaceId, deployment.resourceId, { env: env.slug })
				}
				tried={tried}
				commandOpen={commandOpen}
				holder={taken.holder(draft)}
				onTry={() => {
					setTried(true);
				}}
				onCommandOpen={setCommandOpen}
				onImage={(image) => {
					deployment.clearError();
					if (!store.setImage(draft.id, image)) toast.error("Couldn't save changes to this draft");
				}}
				onEdit={(edits) => {
					deployment.clearError();
					persist(store.edit(draft.id, edits));
				}}
				onDeployed={(resourceId) => {
					store.discard(draft.id);
					void navigate(resourcePath(orgId, workspaceId, resourceId, { env: env.slug }));
				}}
				onDiscard={() => {
					setDiscarding(true);
				}}
			/>
		);
	})();
	return (
		<>
			{header}
			{body}
			{draft !== undefined && (
				<DiscardDraftDialog
					name={draft.name}
					open={discarding}
					onDiscard={() => {
						store.discard(draft.id);
						close();
					}}
					onClose={() => {
						setDiscarding(false);
					}}
				/>
			)}
		</>
	);
}

function CollapsibleRow({
	title,
	summary,
	open,
	onOpenChange,
	children,
}: {
	title: string;
	summary: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	children: ReactNode;
}) {
	return (
		<Collapsible open={open} onOpenChange={onOpenChange} className="flex flex-col gap-3">
			<CollapsibleTrigger className="flex min-h-6 w-full items-center gap-2.5 rounded-sm text-left focus-visible:ring-2 focus-visible:ring-fg3 focus-visible:outline-none">
				<span className="text-[14px] font-medium">{title}</span>
				<span className="min-w-0 flex-1 truncate text-[12.5px] text-fg3">{summary}</span>
				{open ? <ChevronDownIcon className="size-3.5 text-fg3" /> : <ChevronRightIcon className="size-3.5 text-fg3" />}
			</CollapsibleTrigger>
			<CollapsibleContent className="flex flex-col gap-3">{children}</CollapsibleContent>
		</Collapsible>
	);
}

function DrawerSection({ title, actions, children }: { title: ReactNode; actions?: ReactNode; children?: ReactNode }) {
	return (
		<section className="flex flex-col gap-3">
			<div className="flex min-h-[30px] flex-wrap items-center justify-between gap-x-3 gap-y-2">
				<h3 className="m-0 text-[14px] font-medium tracking-[-0.01em]">{title}</h3>
				{actions}
			</div>
			{children}
		</section>
	);
}

function defaultHealth(defaults: DefaultServiceConfig): HealthForm {
	const hc = defaults.healthCheck;
	const num = (n: number | undefined) => (n === undefined || n === 0 ? "" : n.toString());
	return {
		enabled: (hc?.path ?? "") !== "",
		path: hc?.path ?? "",
		interval: num(hc?.intervalSeconds),
		timeout: num(hc?.timeoutSeconds),
		failAfter: num(hc?.failureThreshold),
	};
}

function ServiceForm({
	env,
	draft,
	defaults,
	sizes,
	schemaUrl,
	regions,
	defaultRegion,
	appDomains,
	deployment,
	resourceHref,
	tried,
	commandOpen,
	holder,
	onTry,
	onCommandOpen,
	onImage,
	onEdit,
	onDeployed,
	onDiscard,
}: {
	env: Environment;
	draft: Draft;
	defaults: DefaultServiceConfig;
	sizes: SizeOptions;
	schemaUrl: string;
	regions: RegionInfo[];
	defaultRegion: string;
	appDomains: AppDomain[];
	deployment: ReturnType<typeof useServiceDeploy>;
	resourceHref: string | undefined;
	tried: boolean;
	commandOpen: boolean;
	holder: string | null;
	onTry: () => void;
	onCommandOpen: (open: boolean) => void;
	onImage: (image: string) => void;
	onEdit: (edits: DraftEdits) => void;
	onDeployed: (resourceId: string) => void;
	onDiscard: () => void;
}) {
	const { edits, image, name, slug } = draft;
	const { deployError, deploy, isPending, resourceId } = deployment;
	const [scalingOpen, setScalingOpen] = useState(false);
	const [sizeOpen, setSizeOpen] = useState(false);
	const [varsOpen, setVarsOpen] = useState(false);

	const selected = draftRegions(draft, regions, defaultRegion);
	const regionNames = [...new Set([...regions.map((region) => region.region), ...selected])];
	const size = draftSizing(draft, defaults);
	const served = servedDomains(appDomains, selected);
	const form: NewResourceForm = {
		image,
		name,
		slug,
		description: edits.description ?? "",
		regions: selected,
		cpu: size.cpu,
		memory: size.memory,
		replicas: size.replicas,
		maxReplicas: size.maxReplicas,
		cpuTarget: size.cpuTarget,
		isPublic: draftIsPublic(draft),
		subdomain: edits.subdomain ?? slug,
		appDomain: served.find((d) => d === edits.appDomain) ?? served[0] ?? "",
		port: edits.port ?? "",
		vars: edits.vars ?? [],
		health: edits.health ?? defaultHealth(defaults),
		command: edits.command ?? "",
		args: edits.args ?? "",
	};
	const imageCheck = useImageCheck(env.workspaceId, image);
	const check = useDomainAvailability(form.isPublic ? form.subdomain : "", form.appDomain, []);
	const errors = validateForm(form, check.state, sizes.replicas?.max ?? Number.POSITIVE_INFINITY);
	const shown = (e: string | null, field: keyof NewResourceForm) => (tried || field in edits ? e : null);
	const apiError = (field: ApiField) => (deployError?.field?.field === field ? deployError.field.message : null);
	const subdomainError =
		check.state !== "empty" ? null : form.subdomain === "" ? shown(errors.subdomain, "subdomain") : errors.subdomain;
	const set = onEdit;
	const setHealth = (patch: Partial<HealthForm>) => {
		set({ health: { ...form.health, ...patch } });
	};
	const blocker = firstError(errors);
	const footerBlocker = firstError({ ...errors, image: null });

	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				onTry();
				if (errors.scaling !== null) setScalingOpen(true);
				if (errors.env !== null) setVarsOpen(true);
				if (blocker !== null || isPending || resourceId !== undefined) return;
				void deploy(form, defaults).then(({ error, resourceId: createdResourceId }) => {
					if (error === null && createdResourceId !== undefined) {
						onDeployed(createdResourceId);
						return;
					}
					const field = error?.field?.field;
					if (field !== undefined && COMMAND_FIELDS.has(field)) onCommandOpen(true);
					if (field === "replicas") setScalingOpen(true);
					if (field === "cpu" || field === "memory") setSizeOpen(true);
				});
			}}
		>
			<SheetBody className="gap-0 py-0 [&>*]:border-b [&>*]:border-line [&>*]:py-5 [&>*:last-child]:border-b-0">
				{holder !== null && resourceId === undefined && !isPending && (
					<div>
						<Notice>
							{`${holder} already uses this name or slug in this workspace. Deploying this draft will fail; discard it and start again with another name.`}
						</Notice>
					</div>
				)}
				<div>
					<Field
						label="Container image"
						error={errors.image ?? apiError("image") ?? imageCheck.error}
						hint={imageCheck.checking ? "Checking the image…" : undefined}
					>
						<Input
							value={image}
							className="font-mono"
							autoComplete="off"
							spellCheck={false}
							aria-invalid={(errors.image ?? apiError("image") ?? imageCheck.error) !== null}
							onChange={(e) => {
								onImage(e.target.value);
							}}
						/>
					</Field>
				</div>
				<DrawerSection title="Regions">
					{regionNames.length === 0 ? (
						<span className="text-[13px] text-fg3">No cluster serves {env.name} yet.</span>
					) : (
						<RegionCards
							aria-label="Regions"
							value={selected}
							onValueChange={(next: string[]) => {
								if (next.length > 0) set({ regions: next });
							}}
						>
							{regionNames.map((regionName) => {
								const region = regions.find((info) => info.region === regionName);
								const last = selected.includes(regionName) && selected.length === 1;
								return (
									<RegionCard
										key={regionName}
										name={regionName}
										value={regionName}
										health={region}
										configured={selected.includes(regionName)}
										aria-disabled={last || undefined}
										title={last ? "A resource needs at least one region" : undefined}
									/>
								);
							})}
						</RegionCards>
					)}
					{shown(errors.regions, "regions") !== null && (
						<span className="text-[12.5px] text-bad-fg">{errors.regions}</span>
					)}
				</DrawerSection>

				<DrawerSection
					title="Networking"
					actions={
						<ToggleGroup
							variant="segmented"
							aria-label="Networking"
							value={[form.isPublic ? NETWORK_PUBLIC : NETWORK_INTERNAL]}
							onValueChange={(v: string[]) => {
								if (v.length > 0) set({ isPublic: v.includes(NETWORK_PUBLIC) });
							}}
						>
							<ToggleGroupItem value={NETWORK_INTERNAL}>Internal</ToggleGroupItem>
							<ToggleGroupItem value={NETWORK_PUBLIC}>Public</ToggleGroupItem>
						</ToggleGroup>
					}
				>
					<span className="-mt-1 text-[12.5px] text-fg3">
						{form.isPublic
							? "Gets an HTTPS address on an app domain."
							: `Reachable only by other resources in ${env.name}.`}
					</span>
					<div className="grid grid-cols-1 items-start gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_110px]">
						{form.isPublic && (
							<div className="flex min-w-0 flex-col gap-1.5">
								<span className="text-[13px] font-medium">Address</span>
								<AppDomainInput
									subdomain={form.subdomain}
									appDomain={form.appDomain}
									options={appDomains.filter((d) => served.includes(d.domain))}
									regions={regions}
									check={check}
									placeholder={name}
									onSubdomainChange={(subdomain) => {
										set({ subdomain });
									}}
									onAppDomainChange={(appDomain) => {
										set({ appDomain });
									}}
								/>
								{subdomainError !== null && <span className="text-[12.5px] text-bad-fg">{subdomainError}</span>}
							</div>
						)}
						<Field
							label="Port"
							error={shown(errors.port, "port") ?? apiError("port")}
							className="max-w-[140px] sm:col-start-2 sm:max-w-none"
						>
							<Input
								value={form.port}
								inputMode="numeric"
								className="font-mono"
								aria-invalid={shown(errors.port, "port") !== null || apiError("port") !== null}
								onChange={(e) => {
									set({ port: e.target.value.replace(NON_DIGITS_RE, "") });
								}}
							/>
						</Field>
					</div>
				</DrawerSection>

				<CollapsibleRow
					title="Scaling"
					summary={
						autoscales(form)
							? `Autoscaling, ${form.replicas.toString()} to ${form.maxReplicas.toString()} replicas per region`
							: `${form.replicas.toString()} ${form.replicas === 1 ? "replica" : "replicas"} per region`
					}
					open={scalingOpen}
					onOpenChange={setScalingOpen}
				>
					<AutoscalingSwitch
						value={form}
						replicaRange={sizes.replicas}
						onChange={set}
						className="w-full justify-between"
					/>
					<ScalingFields
						value={form}
						replicaRange={sizes.replicas}
						onChange={set}
						error={errors.scaling ?? apiError("replicas")}
						inDrawer
					/>
				</CollapsibleRow>

				<CollapsibleRow
					title="Size per replica"
					summary={`${stepLabel(sizes.cpu, form.cpu)}, ${stepLabel(sizes.memory, form.memory)}`}
					open={sizeOpen}
					onOpenChange={setSizeOpen}
				>
					<span className="flex items-center gap-1.5 text-[12.5px] text-fg3">
						Cost <SoonTag />
					</span>
					<div className="flex flex-col gap-3">
						<Field label="CPU" aside={<SliderValue text={stepLabel(sizes.cpu, form.cpu)} />} error={apiError("cpu")}>
							<StepSlider
								label="CPU"
								options={sizes.cpu}
								value={form.cpu}
								onChange={(cpu) => {
									set({ cpu });
								}}
							/>
						</Field>
						<Field
							label="Memory"
							aside={<SliderValue text={stepLabel(sizes.memory, form.memory)} />}
							error={apiError("memory")}
						>
							<StepSlider
								label="Memory"
								options={sizes.memory}
								value={form.memory}
								onChange={(memory) => {
									set({ memory });
								}}
							/>
						</Field>
					</div>
				</CollapsibleRow>

				<EnvSection
					open={varsOpen}
					onOpenChange={setVarsOpen}
					rows={form.vars}
					onChange={(vars) => {
						set({ vars });
					}}
				/>

				<CommandSection
					value={form}
					open={commandOpen}
					apiError={apiError}
					onOpenChange={onCommandOpen}
					onChange={set}
				/>

				<HealthSection
					health={form.health}
					apiError={apiError}
					pathError={errors.healthPath}
					timeoutError={errors.healthTimeout}
					onChange={setHealth}
				/>

				<details className="group flex flex-col gap-2.5">
					<summary className="w-fit font-mono text-[13.5px] font-medium">loco.yaml</summary>
					<div className="mt-2.5 flex flex-col gap-2.5">
						<CodeBlock copyLabel="Copy command" lineNumbers={false}>{`loco deploy --env ${env.slug}`}</CodeBlock>
						<CodeBlock copyLabel="Copy loco.yaml">{locoYaml({ schemaUrl, form })}</CodeBlock>
					</div>
				</details>
			</SheetBody>
			<SheetFooter className="flex-wrap items-center">
				<span className="flex min-w-0 flex-1 flex-col gap-0.5 text-[12.5px] text-fg3 max-sm:basis-full">
					<span>
						To <span className="font-mono text-fg2">{env.slug}</span>
					</span>
					<FooterError blocker={resourceId === undefined && tried ? footerBlocker : null} deployError={deployError} />
				</span>
				<Button type="button" variant="destructive-outline" disabled={isPending} onClick={onDiscard}>
					Discard draft
				</Button>
				{resourceHref !== undefined && (
					<Button variant="outline" nativeButton={false} render={<Link to={resourceHref} />}>
						Open resource
					</Button>
				)}
				<Button type="submit" disabled={isPending || resourceId !== undefined}>
					{isPending && <Spinner aria-hidden />}
					{`Deploy ${name}`}
				</Button>
			</SheetFooter>
		</form>
	);
}

function deployErrorText(error: DeployError | null): string | null {
	if (error === null) return null;
	if (error.field === null) return error.message;
	return error.field.field === "image" ? null : `${API_FIELD_LABEL[error.field.field]}: ${error.field.message}`;
}

function FooterError({ blocker, deployError }: { blocker: string | null; deployError: DeployError | null }) {
	const message = blocker ?? deployErrorText(deployError);
	if (message === null) return null;
	return (
		<span role="alert" className="text-bad-fg">
			{message}
		</span>
	);
}

function EnvSection({
	rows,
	open,
	onOpenChange,
	onChange,
}: {
	rows: EnvRow[];
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onChange: (rows: EnvRow[]) => void;
}) {
	const rowErrors = envRowErrors(rows);
	const [pasteOpen, setPasteOpen] = useState(false);
	const [pasteText, setPasteText] = useState("");
	const [focusId, setFocusId] = useState<string | null>(null);
	const parsed = parseDotEnv(pasteText);
	const pasted = parsed.pairs;
	const newId = () => crypto.randomUUID();
	const update = (id: string, patch: Partial<EnvRow>) => {
		onChange(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
	};
	return (
		<CollapsibleRow title="Variables" summary={variablesSummary(rows.length)} open={open} onOpenChange={onOpenChange}>
			<Collapsible open={pasteOpen} onOpenChange={setPasteOpen} className="flex flex-col gap-3">
				<span className="flex gap-1.5">
					<CollapsibleTrigger render={<Button variant="outline" size="sm" />}>
						<ClipboardPasteIcon />
						Paste .env
					</CollapsibleTrigger>
					<Button
						type="button"
						variant="outline"
						size="sm"
						onClick={() => {
							const id = newId();
							setFocusId(id);
							onChange([...rows, { id, key: "", value: "" }]);
						}}
					>
						<PlusIcon />
						Add
					</Button>
				</span>
				<CollapsibleContent className="flex flex-col gap-2">
					<Textarea
						autoFocus
						rows={5}
						value={pasteText}
						aria-label="Paste .env"
						className="font-mono"
						placeholder="KEY=value"
						spellCheck={false}
						onChange={(e) => {
							setPasteText(e.target.value);
						}}
					/>
					<DotEnvNotes notes={dotEnvNotes(parsed, new Set(rows.map((r) => r.key.trim())))} />
					<div className="flex justify-end">
						<Button
							type="button"
							size="sm"
							disabled={pasted.length === 0}
							onClick={() => {
								onChange(mergeEnv(rows, pasted, newId));
								setPasteText("");
								setPasteOpen(false);
							}}
						>
							{pasted.length === 0 ? "Add" : `Add ${pasted.length.toString()}`}
						</Button>
					</div>
				</CollapsibleContent>
			</Collapsible>
			{rows.length === 0 ? (
				<span className="text-[12.5px] text-fg3">None yet.</span>
			) : (
				<ul aria-label="Variables" className="m-0 list-none overflow-hidden rounded-[10px] border border-line p-0">
					{rows.map((r) => {
						const label = r.key.trim() === "" ? "variable" : r.key.trim();
						const rowError = rowErrors.get(r.id);
						return (
							<li
								key={r.id}
								data-invalid={rowError !== undefined || undefined}
								className={cn(
									"grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] items-center border-b border-line last:border-b-0 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto]",
									rowError !== undefined && "bg-bad-bg",
								)}
							>
								<span className="flex min-w-0 items-center border-r border-line">
									<Input
										autoFocus={r.id === focusId}
										value={r.key}
										placeholder="KEY"
										aria-label="Variable name"
										autoComplete="off"
										spellCheck={false}
										className={ENV_CELL}
										onChange={(e) => {
											update(r.id, { key: e.target.value });
										}}
									/>
								</span>
								<Input
									value={r.value}
									placeholder="value"
									aria-label={`Value of ${label}`}
									autoComplete="off"
									spellCheck={false}
									className={ENV_CELL}
									onChange={(e) => {
										update(r.id, { value: e.target.value });
									}}
								/>
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									className="mr-1.5"
									aria-label={`Remove ${label}`}
									onClick={() => {
										onChange(rows.filter((x) => x.id !== r.id));
									}}
								>
									<Trash2Icon />
								</Button>
								{rowError !== undefined && (
									<span className="col-span-full border-t border-line px-3 py-1.5 text-[12.5px] text-bad-fg">
										{rowError}
									</span>
								)}
							</li>
						);
					})}
				</ul>
			)}
		</CollapsibleRow>
	);
}

function variablesSummary(count: number): string {
	if (count === 0) return "None";
	return `${count.toString()} ${count === 1 ? "variable" : "variables"}`;
}

const ENV_CELL =
	"h-[38px] min-w-0 flex-1 rounded-none border-0 bg-transparent font-mono focus-visible:bg-panel focus-visible:ring-2 focus-visible:ring-fg3 focus-visible:ring-inset dark:bg-transparent";

function CommandSection({
	value,
	open,
	apiError,
	onOpenChange,
	onChange,
}: {
	value: CommandValue;
	open: boolean;
	apiError: (field: ApiField) => string | null;
	onOpenChange: (open: boolean) => void;
	onChange: (patch: Partial<CommandValue>) => void;
}) {
	return (
		<CollapsibleRow title="Start command" summary={commandSummary(value)} open={open} onOpenChange={onOpenChange}>
			<CommandFields
				value={value}
				errors={{ command: apiError("command"), args: apiError("args") }}
				onChange={onChange}
			/>
		</CollapsibleRow>
	);
}

function HealthSection({
	health,
	apiError,
	pathError,
	timeoutError,
	onChange,
}: {
	health: HealthForm;
	apiError: (field: ApiField) => string | null;
	pathError: string | null;
	timeoutError: string | null;
	onChange: (patch: Partial<HealthForm>) => void;
}) {
	const numeric = (label: string, field: "interval" | "timeout" | "failAfter") => {
		const error = (field === "timeout" ? timeoutError : null) ?? apiError(field);
		return (
			<Field label={label} error={error}>
				<Input
					value={health[field]}
					inputMode="numeric"
					className="font-mono"
					aria-invalid={error !== null}
					onChange={(e) => {
						onChange({ [field]: e.target.value.replace(NON_DIGITS_RE, "") });
					}}
				/>
			</Field>
		);
	};
	return (
		<DrawerSection
			title="Health check"
			actions={
				<Switch
					aria-label="Health check"
					checked={health.enabled}
					onCheckedChange={(enabled) => {
						onChange({ enabled, path: health.path === "" ? "/healthz" : health.path });
					}}
				/>
			}
		>
			<span className="-mt-1 text-[12.5px] text-fg3">
				Optional. Without one, a replica counts as ready once its process starts.
			</span>
			{health.enabled && (
				<div className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
					<Field
						label="Path"
						className="col-span-full"
						error={pathError ?? apiError("path")}
						hint="Loco waits for this path to return 2xx before sending traffic."
					>
						<Input
							value={health.path}
							className="font-mono"
							placeholder="/healthz"
							aria-invalid={(pathError ?? apiError("path")) !== null}
							onChange={(e) => {
								onChange({ path: e.target.value });
							}}
						/>
					</Field>
					{numeric("Interval, sec", "interval")}
					{numeric("Timeout, sec", "timeout")}
					{numeric("Fail after, checks", "failAfter")}
				</div>
			)}
		</DrawerSection>
	);
}
