import { useQuery as useRpcQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, CircleAlertIcon, LockIcon, PlusIcon } from "lucide-react";
import { useState } from "react";
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
import { getErrorMessage } from "@/lib/error-handler";
import {
	errorCount,
	imageError,
	locoYaml,
	looksSecret,
	nameFromImage,
	parseEnvText,
	servedDomains,
	validateForm,
	type NewResourceForm,
} from "@/lib/new-resource";
import { slug } from "@/lib/slug";
import { DeployRunView, useServiceDeploy } from "@/pages/resources/NewServiceDeploy";

const HEALTHY = "healthy";
const MIN_REPLICAS = 1;
const SKELETON_ROWS = 6;

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
	const [step, setStep] = useState<Step>("image");
	const [image, setImage] = useState("");
	const close = (open: boolean) => {
		if (!open) onClose();
	};
	return (
		<>
			<Dialog open={step === "image"} onOpenChange={close}>
				<DialogContent title="New service">
					<ImageStep
						orgId={orgId}
						workspaceId={workspaceId}
						env={env}
						image={image}
						onImage={setImage}
						onContinue={() => {
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
								<SheetTitle>New service</SheetTitle>
								<span className="truncate font-mono text-[12px] text-fg3" title={image.trim()}>
									{image.trim()}
								</span>
							</div>
						</SheetHeader>
						{step === "drawer" && (
							<ServiceDrawer orgId={orgId} workspaceId={workspaceId} env={env} image={image} onCancel={onClose} />
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
	onImage,
	onContinue,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment | undefined;
	image: string;
	onImage: (image: string) => void;
	onContinue: () => void;
}) {
	const scopes = useMyScopes();
	const [tried, setTried] = useState(false);
	const [edited, setEdited] = useState(false);
	const error = imageError(image);
	const shown = tried || edited ? error : null;

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
				if (error === null) onContinue();
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
	onCancel,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	image: string;
	onCancel: () => void;
}) {
	const transport = useTransport();
	const configQuery = useRpcQuery(getConfig, {});
	const regionsQuery = useRpcQuery(listRegions, { environmentId: env.id });
	const domainsQuery = useRpcQuery(listPlatformDomains, { environmentId: env.id });
	const resourcesQuery = useQuery(workspaceResourcesOptions(transport, workspaceId));

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
			defaults={defaults}
			schemaUrl={configQuery.data?.schemaUrl ?? ""}
			regions={regionsQuery.data.regions}
			platformDomains={domainsQuery.data?.platformDomains ?? []}
			taken={new Map((resourcesQuery.data?.resources ?? []).map((r) => [r.slug, r.name]))}
			onCancel={onCancel}
		/>
	);
}

function ServiceForm({
	orgId,
	workspaceId,
	env,
	image,
	defaults,
	schemaUrl,
	regions,
	platformDomains,
	taken,
	onCancel,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	image: string;
	defaults: DefaultServiceConfig;
	schemaUrl: string;
	regions: RegionInfo[];
	platformDomains: PlatformDomain[];
	taken: ReadonlyMap<string, string>;
	onCancel: () => void;
}) {
	const [edits, setEdits] = useState<Partial<NewResourceForm>>({});
	const [tried, setTried] = useState(false);
	const { run, createError, deploy } = useServiceDeploy(workspaceId, env, defaults);

	const name = edits.name ?? nameFromImage(image, new Set(taken.keys()));
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
		envText: edits.envText ?? "",
		healthPath: edits.healthPath ?? defaults.healthCheck?.path ?? "",
	};
	const check = useDomainAvailability(form.isPublic ? form.subdomain : "", form.platformDomain, []);
	const errors = validateForm(form, taken, check.state);
	const shown = (e: string | null, field: keyof NewResourceForm) => (tried || field in edits ? e : null);
	const nameError = form.name === "" ? shown(errors.name, "name") : errors.name;
	const subdomainError =
		check.state !== "empty" ? null : form.subdomain === "" ? shown(errors.subdomain, "subdomain") : errors.subdomain;
	const set = (patch: Partial<NewResourceForm>) => {
		setEdits((prev) => ({ ...prev, ...patch }));
	};
	const vars = parseEnvText(form.envText).vars;
	const invalid = errorCount(errors);

	if (run !== null) {
		return <DeployRunView run={run} env={env} defaults={defaults} orgId={orgId} workspaceId={workspaceId} />;
	}

	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				setTried(true);
				if (invalid === 0) void deploy(form);
			}}
		>
			<SheetBody className="gap-0 py-0 [&>*]:border-b [&>*]:border-line [&>*]:py-[18px] [&>*:last-child]:border-b-0">
				<div className="flex flex-col gap-[18px]">
					<Field label="Name" error={nameError}>
						<Input
							value={name}
							className="font-mono"
							placeholder="web"
							autoComplete="off"
							spellCheck={false}
							aria-invalid={nameError !== null}
							onChange={(e) => {
								set({ name: e.target.value });
							}}
						/>
					</Field>
					<div className="flex flex-col gap-1.5">
						<span className="text-[13px] font-medium">Regions</span>
						{regions.length === 0 ? (
							<span className="text-[13px] text-fg3">No cluster serves {env.name} yet.</span>
						) : (
							<ToggleGroup
								multiple
								variant="outline"
								aria-label="Regions"
								className="flex-wrap"
								value={selected}
								onValueChange={(v: string[]) => {
									set({ regions: v });
								}}
							>
								{regions.map((r) => (
									<ToggleGroupItem key={r.region} value={r.region} className="gap-[7px] font-mono text-[12.5px]">
										{selected.includes(r.region) ? <CheckIcon /> : <PlusIcon />}
										{r.region}
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
					</div>
				</div>

				<div className="flex flex-col gap-3.5">
					<div className="grid grid-cols-2 gap-x-[18px] gap-y-3.5">
						<Field label="CPU per replica" error={shown(errors.cpu, "cpu")}>
							<Input
								value={form.cpu}
								className="font-mono"
								aria-invalid={shown(errors.cpu, "cpu") !== null}
								onChange={(e) => {
									set({ cpu: e.target.value });
								}}
							/>
						</Field>
						<Field label="Memory per replica" error={shown(errors.memory, "memory")}>
							<Input
								value={form.memory}
								className="font-mono"
								aria-invalid={shown(errors.memory, "memory") !== null}
								onChange={(e) => {
									set({ memory: e.target.value });
								}}
							/>
						</Field>
					</div>
					<div className="flex flex-wrap gap-x-[18px] gap-y-3.5">
						<div className="flex flex-col gap-1.5">
							<span className="text-[13px] font-medium">Min replicas</span>
							<Stepper
								label="Min replicas"
								value={form.replicas}
								min={MIN_REPLICAS}
								max={Number.POSITIVE_INFINITY}
								onChange={(replicas) => {
									set({ replicas });
								}}
							/>
						</div>
						<div className="flex flex-col gap-1.5">
							<span className="flex items-center gap-2 text-[13px] font-medium text-fg3">
								Max replicas <SoonTag />
							</span>
							<Stepper
								label="Max replicas"
								value={form.replicas}
								min={MIN_REPLICAS}
								max={Number.POSITIVE_INFINITY}
								disabled
								onChange={() => undefined}
							/>
						</div>
					</div>
				</div>

				<div className="flex flex-col gap-3.5">
					<div className="flex items-center justify-between gap-4">
						<span className="flex flex-col gap-0.5">
							<span className="text-[13px] font-medium">Public</span>
							<span className="text-[12.5px] text-fg3">
								Give it an HTTPS address. Turn off for workers and internal services.
							</span>
						</span>
						<Switch
							aria-label="Public"
							checked={form.isPublic}
							onCheckedChange={(isPublic: boolean) => {
								set({ isPublic });
							}}
						/>
					</div>
					{form.isPublic && (
						<div className="flex min-w-0 flex-col gap-1.5">
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
					<Field label="Port" error={shown(errors.port, "port")} className="max-w-[140px]">
						<Input
							value={form.port}
							inputMode="numeric"
							className="font-mono"
							aria-invalid={shown(errors.port, "port") !== null}
							onChange={(e) => {
								set({ port: e.target.value });
							}}
						/>
					</Field>
				</div>

				<div className="flex flex-col gap-3.5">
					<Field
						label="Environment variables"
						hint="Paste a .env file or one KEY=value per line."
						error={shown(errors.env, "envText")}
					>
						<Textarea
							rows={4}
							value={form.envText}
							className="font-mono"
							placeholder={"DATABASE_URL=postgres://…\nLOG_LEVEL=info"}
							spellCheck={false}
							aria-invalid={shown(errors.env, "envText") !== null}
							onChange={(e) => {
								set({ envText: e.target.value });
							}}
						/>
					</Field>
					{vars.length > 0 && (
						<ul aria-label="Variables" className="m-0 flex list-none flex-wrap gap-1.5 p-0">
							{vars.map(([key]) => (
								<li key={key}>
									<Badge tone="muted" className="font-mono">
										{looksSecret(key) && <LockIcon aria-label="Looks like a secret" />}
										{key}
									</Badge>
								</li>
							))}
						</ul>
					)}
					<div className="flex items-center justify-between gap-4">
						<span className="flex flex-col gap-0.5">
							<span className="flex items-center gap-2 text-[13px] font-medium text-fg3">
								Secret <SoonTag />
							</span>
							<span className="text-[12.5px] text-fg3">Store a variable as a secret instead of plain text.</span>
						</span>
						<Switch disabled checked={false} aria-label="Secret" />
					</div>
					<Field
						label="Health check path"
						error={shown(errors.healthPath, "healthPath")}
						hint="Loco waits for this path to return 2xx before sending traffic. Leave empty for none."
					>
						<Input
							value={form.healthPath}
							className="max-w-[240px] font-mono"
							placeholder="/healthz"
							aria-invalid={shown(errors.healthPath, "healthPath") !== null}
							onChange={(e) => {
								set({ healthPath: e.target.value });
							}}
						/>
					</Field>
				</div>

				<details className="group flex flex-col gap-2.5">
					<summary className="w-fit text-[13px] font-medium">loco.yaml</summary>
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
					{tried && invalid > 0 && (
						<span role="alert" className="flex items-center gap-1.5 text-bad-fg">
							<CircleAlertIcon className="size-[13px]" />
							Fix {invalid.toString()} {invalid === 1 ? "field" : "fields"} to deploy
						</span>
					)}
					{createError !== null && (
						<span role="alert" className="text-bad-fg">
							{createError}
						</span>
					)}
				</span>
				<Button type="button" variant="outline" onClick={onCancel}>
					Cancel
				</Button>
				<Button type="submit">{form.name === "" ? "Deploy" : `Deploy ${form.name}`}</Button>
			</SheetFooter>
		</form>
	);
}
