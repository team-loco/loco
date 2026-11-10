import { useQuery as useRpcQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { ChevronDownIcon, ChevronRightIcon, ClipboardPasteIcon, LockIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useId, useRef, useState, type ReactNode } from "react";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import type { PlatformDomain } from "@gen/loco/domain/v1/domain_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { Dialog, DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { PlatformDomainInput } from "@/components/design/PlatformDomainInput";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { Stepper } from "@/components/design/Stepper";
import { Switch } from "@/components/design/Switch";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useDomainAvailability } from "@/hooks/useDomainAvailability";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { parseDotEnv } from "@/lib/dotenv";
import { getErrorMessage } from "@/lib/error-handler";
import {
	firstError,
	healthSummary,
	imageError,
	locoYaml,
	looksSecret,
	mergeEnv,
	nameError,
	nameFromImage,
	servedDomains,
	validateForm,
	type ApiField,
	type EnvRow,
	type HealthForm,
	type NewResourceForm,
} from "@/lib/new-resource";
import { slug } from "@/lib/slug";
import { DeployRunView, useServiceDeploy, type DeployError } from "@/pages/resources/NewServiceDeploy";

const HEALTHY = "healthy";
const MIN_REPLICAS = 1;
const SKELETON_ROWS = 6;
const NON_DIGITS_RE = /\D/g;
const NETWORK_PUBLIC = "public";
const NETWORK_INTERNAL = "internal";

const API_FIELD_LABEL: Record<ApiField, string> = {
	port: "Port",
	path: "Health check path",
	interval: "Health check interval",
	timeout: "Health check timeout",
	failAfter: "Health check fail after",
	cpu: "CPU",
	memory: "Memory",
	replicas: "Replicas",
};
const HEALTH_FIELDS: ReadonlySet<ApiField> = new Set(["path", "interval", "timeout", "failAfter"]);

type Step = "image" | "drawer";

export function NewService({
	orgId,
	workspaceId,
	env,
	onClose,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment | undefined;
	onClose: () => void;
}) {
	const transport = useTransport();
	const resourcesQuery = useQuery(workspaceResourcesOptions(transport, workspaceId));
	const [step, setStep] = useState<Step>("image");
	const [image, setImage] = useState("");
	const [editedName, setEditedName] = useState<string | null>(null);
	const taken = new Map((resourcesQuery.data?.resources ?? []).map((r) => [r.slug, r.name]));
	const name = editedName ?? nameFromImage(image, new Set(taken.keys()));
	const close = (open: boolean) => {
		if (!open) onClose();
	};
	return (
		<>
			<Dialog open={step === "image"} onOpenChange={close}>
				<DialogContent title="New resource">
					<ImageStep
						orgId={orgId}
						workspaceId={workspaceId}
						env={env}
						image={image}
						name={name}
						taken={taken}
						onImage={setImage}
						onName={setEditedName}
						onContinue={() => {
							setEditedName(name);
							setStep("drawer");
						}}
					/>
				</DialogContent>
			</Dialog>
			{env !== undefined && (
				<Sheet open={step === "drawer"} onOpenChange={close}>
					<SheetContent size="wide">
						<SheetHeader>
							<div className="flex min-w-0 flex-col gap-0.5">
								<SheetTitle className="truncate">{name}</SheetTitle>
								<span className="truncate font-mono text-[12px] text-fg3" title={image.trim()}>
									{image.trim()}
								</span>
							</div>
						</SheetHeader>
						{step === "drawer" && (
							<ServiceDrawer
								orgId={orgId}
								workspaceId={workspaceId}
								env={env}
								image={image}
								name={name}
								onClose={onClose}
							/>
						)}
					</SheetContent>
				</Sheet>
			)}
		</>
	);
}

function ImageStep({
	orgId,
	workspaceId,
	env,
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
	image: string;
	name: string;
	taken: ReadonlyMap<string, string>;
	onImage: (image: string) => void;
	onName: (name: string) => void;
	onContinue: () => void;
}) {
	const scopes = useMyScopes();
	const [tried, setTried] = useState(false);
	const [edited, setEdited] = useState(false);
	const error = imageError(image);
	const shown = tried || edited ? error : null;
	const nameErr = nameError(name, taken);

	if (scopes.isLoading) {
		return (
			<DialogBody>
				<Skeleton className="h-16 w-full" />
			</DialogBody>
		);
	}
	const notice =
		scopes.workspaceLevel(orgId, workspaceId) < scopeLevel(Scope.WRITE)
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
				setTried(true);
				if (error === null && nameErr === null) onContinue();
			}}
		>
			<DialogBody>
				<Field label="Container image" hint="A public image. Private registries aren't supported yet." error={shown}>
					<Input
						autoFocus
						value={image}
						className="font-mono"
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
							className="font-mono"
							autoComplete="off"
							spellCheck={false}
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
				<Button type="submit">Continue</Button>
			</DialogFooter>
		</form>
	);
}

function ServiceDrawer({
	orgId,
	workspaceId,
	env,
	image,
	name,
	onClose,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	image: string;
	name: string;
	onClose: () => void;
}) {
	const configQuery = useRpcQuery(getConfig, {});
	const regionsQuery = useRpcQuery(listRegions, { environmentId: env.id });
	const domainsQuery = useRpcQuery(listPlatformDomains, { environmentId: env.id });

	const loadError = configQuery.error ?? regionsQuery.error ?? domainsQuery.error;
	const defaults = configQuery.data?.serviceDefaults;
	if (loadError !== null) {
		return (
			<SheetBody>
				<EmptyState title="Couldn't load the form">{getErrorMessage(loadError, "Failed to load")}</EmptyState>
			</SheetBody>
		);
	}
	if (defaults === undefined || regionsQuery.data === undefined) {
		return (
			<SheetBody>
				{Array.from({ length: SKELETON_ROWS }, (_, i) => (
					<Skeleton key={i} className="h-14 w-full" />
				))}
			</SheetBody>
		);
	}
	return (
		<ServiceForm
			orgId={orgId}
			workspaceId={workspaceId}
			env={env}
			image={image}
			name={name}
			defaults={defaults}
			schemaUrl={configQuery.data?.schemaUrl ?? ""}
			regions={regionsQuery.data.regions}
			platformDomains={domainsQuery.data?.platformDomains ?? []}
			onClose={onClose}
		/>
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
		path: hc?.path ?? "",
		interval: num(hc?.intervalSeconds),
		timeout: num(hc?.timeoutSeconds),
		failAfter: num(hc?.failureThreshold),
	};
}

function ServiceForm({
	orgId,
	workspaceId,
	env,
	image,
	name,
	defaults,
	schemaUrl,
	regions,
	platformDomains,
	onClose,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	image: string;
	name: string;
	defaults: DefaultServiceConfig;
	schemaUrl: string;
	regions: RegionInfo[];
	platformDomains: PlatformDomain[];
	onClose: () => void;
}) {
	const [edits, setEdits] = useState<Partial<NewResourceForm>>({});
	const [tried, setTried] = useState(false);
	const [healthOpen, setHealthOpen] = useState(false);
	const { run, deployError, deploy, clearError } = useServiceDeploy(workspaceId, env, defaults);

	const selected = edits.regions ?? regions.filter((r) => r.isDefault).map((r) => r.region);
	const served = servedDomains(platformDomains, selected);
	const form: NewResourceForm = {
		image,
		name,
		regions: selected,
		cpu: edits.cpu ?? defaults.cpu,
		memory: edits.memory ?? defaults.memory,
		replicas: edits.replicas ?? defaults.minReplicas,
		isPublic: edits.isPublic ?? true,
		subdomain: edits.subdomain ?? slug(name),
		platformDomain: served.find((d) => d === edits.platformDomain) ?? served[0] ?? "",
		port: edits.port ?? String(defaults.routing?.port ?? ""),
		vars: edits.vars ?? [],
		health: edits.health ?? defaultHealth(defaults),
	};
	const check = useDomainAvailability(form.isPublic ? form.subdomain : "", form.platformDomain, []);
	const errors = validateForm(form, check.state);
	const shown = (e: string | null, field: keyof NewResourceForm) => (tried || field in edits ? e : null);
	const apiError = (field: ApiField) => (deployError?.field?.field === field ? deployError.field.message : null);
	const subdomainError =
		check.state !== "empty" ? null : form.subdomain === "" ? shown(errors.subdomain, "subdomain") : errors.subdomain;
	const set = (patch: Partial<NewResourceForm>) => {
		clearError();
		setEdits((prev) => ({ ...prev, ...patch }));
	};
	const setHealth = (patch: Partial<HealthForm>) => {
		set({ health: { ...form.health, ...patch } });
	};
	const blocker = firstError(errors);

	if (run !== null) {
		return <DeployRunView run={run} env={env} orgId={orgId} workspaceId={workspaceId} onDone={onClose} />;
	}

	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				setTried(true);
				if (blocker !== null) return;
				void deploy(form).then((err) => {
					const field = err?.field?.field;
					if (field !== undefined && HEALTH_FIELDS.has(field)) setHealthOpen(true);
				});
			}}
		>
			<SheetBody className="gap-0 py-0 [&>*]:border-b [&>*]:border-line [&>*]:py-5 [&>*:last-child]:border-b-0">
				<DrawerSection title="Regions">
					{regions.length === 0 ? (
						<span className="text-[13px] text-fg3">No cluster serves {env.name} yet.</span>
					) : (
						<ToggleGroup
							multiple
							variant="outline"
							aria-label="Regions"
							className="grid w-full grid-cols-2 gap-2 sm:grid-cols-3"
							value={selected}
							onValueChange={(v: string[]) => {
								set({ regions: v });
							}}
						>
							{regions.map((r) => (
								<ToggleGroupItem
									key={r.region}
									value={r.region}
									className="h-[38px] w-full min-w-0 gap-1.5 rounded-lg border-line2 px-3 font-mono text-[12.5px] font-normal text-fg3 hover:bg-transparent hover:text-foreground aria-pressed:border-foreground aria-pressed:bg-raise aria-pressed:text-foreground aria-pressed:ring-1 aria-pressed:ring-foreground aria-pressed:ring-inset"
								>
									<span className="truncate">{r.region}</span>
									{r.healthStatus !== "" && r.healthStatus !== HEALTHY && (
										<Badge size="sm" tone="warn">
											{r.healthStatus}
										</Badge>
									)}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
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
							? "Gets an HTTPS address on a platform domain."
							: `Reachable only by other resources in ${env.name}.`}
					</span>
					<div className="grid grid-cols-1 items-start gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_110px]">
						{form.isPublic && (
							<div className="flex min-w-0 flex-col gap-1.5">
								<span className="text-[13px] font-medium">Address</span>
								<PlatformDomainInput
									subdomain={form.subdomain}
									platformDomain={form.platformDomain}
									options={platformDomains.filter((d) => served.includes(d.domain))}
									check={check}
									placeholder={slug(name)}
									onSubdomainChange={(subdomain) => {
										set({ subdomain });
									}}
									onPlatformDomainChange={(platformDomain) => {
										set({ platformDomain });
									}}
								/>
								{subdomainError !== null && <span className="text-[12.5px] text-bad-fg">{subdomainError}</span>}
							</div>
						)}
						<Field label="Port" error={apiError("port")} className="max-w-[140px] sm:col-start-2 sm:max-w-none">
							<Input
								value={form.port}
								inputMode="numeric"
								className="font-mono"
								aria-invalid={apiError("port") !== null}
								onChange={(e) => {
									set({ port: e.target.value.replace(NON_DIGITS_RE, "") });
								}}
							/>
						</Field>
					</div>
				</DrawerSection>

				<DrawerSection
					title="Scaling"
					actions={
						<span className="flex items-center gap-2.5 text-[13px] text-fg3">
							Autoscaling <SoonTag />
							<Switch disabled checked={false} aria-label="Autoscaling" />
						</span>
					}
				>
					<div className="flex flex-col gap-1.5">
						<span className="text-[13px] font-medium">Replicas per region</span>
						<Stepper
							label="Replicas per region"
							value={form.replicas}
							min={MIN_REPLICAS}
							max={Number.POSITIVE_INFINITY}
							onChange={(replicas) => {
								set({ replicas });
							}}
						/>
						<span className={apiError("replicas") === null ? "text-[12.5px] text-fg3" : "text-[12.5px] text-bad-fg"}>
							{apiError("replicas") ?? `Runs exactly ${form.replicas.toString()} per region.`}
						</span>
					</div>
				</DrawerSection>

				<DrawerSection
					title="Size per replica"
					actions={
						<span className="flex items-center gap-4 text-[12.5px] text-fg3">
							<span className="flex items-center gap-1.5">
								Sliders <SoonTag />
							</span>
							<span className="flex items-center gap-1.5">
								Cost <SoonTag />
							</span>
						</span>
					}
				>
					<div className="grid grid-cols-2 gap-x-4 gap-y-3">
						<Field label="CPU" error={shown(errors.cpu, "cpu") ?? apiError("cpu")}>
							<Input
								value={form.cpu}
								className="font-mono"
								aria-invalid={(shown(errors.cpu, "cpu") ?? apiError("cpu")) !== null}
								onChange={(e) => {
									set({ cpu: e.target.value });
								}}
							/>
						</Field>
						<Field label="Memory" error={shown(errors.memory, "memory") ?? apiError("memory")}>
							<Input
								value={form.memory}
								className="font-mono"
								aria-invalid={(shown(errors.memory, "memory") ?? apiError("memory")) !== null}
								onChange={(e) => {
									set({ memory: e.target.value });
								}}
							/>
						</Field>
					</div>
				</DrawerSection>

				<EnvSection
					rows={form.vars}
					error={shown(errors.env, "vars")}
					onChange={(vars) => {
						set({ vars });
					}}
				/>

				<HealthSection
					health={form.health}
					open={healthOpen}
					apiError={apiError}
					onOpenChange={setHealthOpen}
					onChange={setHealth}
				/>

				<details className="group flex flex-col gap-2.5">
					<summary className="w-fit text-[14px] font-medium">loco.yaml</summary>
					<div className="mt-2.5 flex flex-col gap-2.5">
						<p className="m-0 text-[12.5px] text-fg3">
							The same service as a <code className="font-mono">loco.yaml</code>. Save it next to your code and run:
						</p>
						<CodeBlock>{`loco deploy --env ${env.name}`}</CodeBlock>
						<CodeBlock>{locoYaml({ schemaUrl, form })}</CodeBlock>
					</div>
				</details>
			</SheetBody>
			<SheetFooter className="flex-wrap items-center">
				<span className="flex min-w-0 flex-1 flex-col gap-0.5 text-[12.5px] text-fg3 max-sm:basis-full">
					<span>
						To <span className="font-mono text-fg2">{env.name}</span>
					</span>
					<FooterError tried={tried} blocker={blocker} deployError={deployError} />
				</span>
				<Button type="button" variant="outline" onClick={onClose}>
					Cancel
				</Button>
				<Button type="submit">{`Deploy ${name}`}</Button>
			</SheetFooter>
		</form>
	);
}

function FooterError({
	tried,
	blocker,
	deployError,
}: {
	tried: boolean;
	blocker: string | null;
	deployError: DeployError | null;
}) {
	const message =
		tried && blocker !== null
			? blocker
			: deployError === null
				? null
				: deployError.field === null
					? deployError.message
					: `${API_FIELD_LABEL[deployError.field.field]}: ${deployError.field.message}`;
	if (message === null) return null;
	return (
		<span role="alert" className="text-bad-fg">
			{message}
		</span>
	);
}

function EnvSection({
	rows,
	error,
	onChange,
}: {
	rows: EnvRow[];
	error: string | null;
	onChange: (rows: EnvRow[]) => void;
}) {
	const nextIdRef = useRef(0);
	const [pasteOpen, setPasteOpen] = useState(false);
	const [pasteText, setPasteText] = useState("");
	const [focusId, setFocusId] = useState<string | null>(null);
	const pasteId = useId();
	const pasted = parseDotEnv(pasteText);
	const newId = () => {
		nextIdRef.current += 1;
		return nextIdRef.current.toString();
	};
	const update = (id: string, patch: Partial<EnvRow>) => {
		onChange(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
	};
	return (
		<DrawerSection
			title={
				<>
					Environment variables <span className="ml-2 font-normal text-fg3">{rows.length}</span>
				</>
			}
			actions={
				<span className="flex gap-1.5">
					<Button
						type="button"
						variant="outline"
						size="sm"
						aria-expanded={pasteOpen}
						aria-controls={pasteId}
						onClick={() => {
							setPasteOpen(!pasteOpen);
						}}
					>
						<ClipboardPasteIcon />
						Paste .env
					</Button>
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
			}
		>
			{pasteOpen && (
				<div id={pasteId} className="flex flex-col gap-2">
					<Textarea
						autoFocus
						rows={5}
						value={pasteText}
						aria-label="Paste .env"
						className="font-mono"
						placeholder={"DATABASE_URL=postgres://…\nLOG_LEVEL=info"}
						spellCheck={false}
						onChange={(e) => {
							setPasteText(e.target.value);
						}}
					/>
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
							{`Add ${pasted.length.toString()}`}
						</Button>
					</div>
				</div>
			)}
			{rows.length === 0 ? (
				<span className="text-[12.5px] text-fg3">None yet.</span>
			) : (
				<ul aria-label="Variables" className="m-0 list-none overflow-hidden rounded-[10px] border border-line p-0">
					{rows.map((r) => {
						const label = r.key.trim() === "" ? "variable" : r.key.trim();
						return (
							<li
								key={r.id}
								className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto] items-center border-b border-line last:border-b-0"
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
									{looksSecret(r.key) && (
										<LockIcon aria-label="Looks like a secret" className="mr-2.5 size-3.5 shrink-0 text-fg3" />
									)}
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
							</li>
						);
					})}
				</ul>
			)}
			{error !== null && <span className="text-[12.5px] text-bad-fg">{error}</span>}
			<div className="flex items-center justify-between gap-4">
				<span className="flex flex-col gap-0.5">
					<span className="flex items-center gap-2 text-[13px] font-medium text-fg3">
						Secret <SoonTag />
					</span>
					<span className="text-[12.5px] text-fg3">Store a variable as a secret instead of plain text.</span>
				</span>
				<Switch disabled checked={false} aria-label="Secret" />
			</div>
		</DrawerSection>
	);
}

const ENV_CELL =
	"h-[38px] min-w-0 flex-1 rounded-none border-0 bg-transparent font-mono focus-visible:bg-panel focus-visible:ring-2 focus-visible:ring-fg3 focus-visible:ring-inset dark:bg-transparent";

function HealthSection({
	health,
	open,
	apiError,
	onOpenChange,
	onChange,
}: {
	health: HealthForm;
	open: boolean;
	apiError: (field: ApiField) => string | null;
	onOpenChange: (open: boolean) => void;
	onChange: (patch: Partial<HealthForm>) => void;
}) {
	const panelId = useId();
	const numeric = (label: string, field: "interval" | "timeout" | "failAfter") => (
		<Field label={label} error={apiError(field)}>
			<Input
				value={health[field]}
				inputMode="numeric"
				className="font-mono"
				aria-invalid={apiError(field) !== null}
				onChange={(e) => {
					onChange({ [field]: e.target.value.replace(NON_DIGITS_RE, "") });
				}}
			/>
		</Field>
	);
	return (
		<section className="flex flex-col gap-3">
			<button
				type="button"
				aria-expanded={open}
				aria-controls={panelId}
				className="flex w-full items-center gap-2.5 rounded-sm text-left focus-visible:ring-2 focus-visible:ring-fg3 focus-visible:outline-none"
				onClick={() => {
					onOpenChange(!open);
				}}
			>
				<span className="text-[14px] font-medium">Health check</span>
				<span className="min-w-0 flex-1 truncate font-mono text-[12px] text-fg3">{healthSummary(health)}</span>
				{open ? <ChevronDownIcon className="size-3.5 text-fg3" /> : <ChevronRightIcon className="size-3.5 text-fg3" />}
			</button>
			{open && (
				<div id={panelId} className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
					<Field
						label="Path"
						className="col-span-full"
						error={apiError("path")}
						hint="Loco waits for this path to return 2xx before sending traffic. Leave empty for none."
					>
						<Input
							value={health.path}
							className="font-mono"
							placeholder="/healthz"
							aria-invalid={apiError("path") !== null}
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
		</section>
	);
}
