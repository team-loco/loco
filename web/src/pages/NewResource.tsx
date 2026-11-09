import { useQuery as useRpcQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, PlusIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Badge } from "@/components/design/Badge";
import { EmptyState } from "@/components/design/EmptyState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { PlatformDomainInput } from "@/components/design/PlatformDomainInput";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { Stepper } from "@/components/design/Stepper";
import { Switch } from "@/components/design/Switch";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDomainAvailability } from "@/hooks/useDomainAvailability";
import { useEnvironments } from "@/hooks/useEnvironment";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { getErrorMessage } from "@/lib/error-handler";
import { parseEnvText, servedDomains, nameFromImage, validateForm, type NewResourceForm } from "@/lib/new-resource";
import { workspacePath } from "@/lib/routes";
import { slug } from "@/lib/slug";
import { SidePanel, type DeployRun } from "@/pages/new-resource/SidePanel";

const HEALTHY = "healthy";

function FormSection({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
	return (
		<section className="flex flex-col gap-4 border-b border-line py-[22px] first:pt-0 last:border-b-0">
			<div className="flex flex-col gap-1">
				<h2 className="m-0 text-[15px] font-medium tracking-[-0.01em]">{title}</h2>
				{hint !== undefined && <p className="m-0 text-[13px] text-fg3">{hint}</p>}
			</div>
			{children}
		</section>
	);
}

function SoonRow({ label, hint }: { label: string; hint: string }) {
	return (
		<div className="flex items-center justify-between gap-4">
			<span className="flex flex-col gap-0.5">
				<span className="flex items-center gap-2 text-[13.5px] font-medium text-fg3">
					{label} <SoonTag />
				</span>
				<span className="text-[12.5px] text-fg3">{hint}</span>
			</span>
			<Switch disabled checked={false} aria-label={label} />
		</div>
	);
}

const grid2 = "grid grid-cols-[repeat(auto-fit,minmax(min(240px,100%),1fr))] gap-x-[18px] gap-y-3.5";

export function NewResource() {
	const transport = useTransport();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const scopes = useMyScopes();
	const { environments, active, setActive, isLoading: envsLoading } = useEnvironments();
	const configQuery = useRpcQuery(getConfig, {});
	const envId = active?.id ?? "";
	const regionsQuery = useRpcQuery(listRegions, { environmentId: envId }, { enabled: envId !== "" });
	const domainsQuery = useRpcQuery(listPlatformDomains, { environmentId: envId }, { enabled: envId !== "" });
	const resourcesQuery = useQuery(workspaceResourcesOptions(transport, workspaceId));

	const [edits, setEdits] = useState<Partial<NewResourceForm>>({});
	const [regionEdit, setRegionEdit] = useState<{ envId: string; regions: string[] } | null>(null);
	const [tried, setTried] = useState(false);
	const [run, setRun] = useState<DeployRun | null>(null);

	const defaults = configQuery.data?.serviceDefaults;
	const regions = regionsQuery.data?.regions ?? [];
	const taken = new Map((resourcesQuery.data?.resources ?? []).map((r) => [r.slug, r.name]));
	const image = edits.image ?? "";
	const name = edits.name ?? nameFromImage(image, new Set(taken.keys()));
	const selected =
		regionEdit?.envId === envId ? regionEdit.regions : regions.filter((r) => r.isDefault).map((r) => r.region);
	const platformDomains = domainsQuery.data?.platformDomains ?? [];
	const served = servedDomains(platformDomains, selected);
	const form: NewResourceForm = {
		image,
		name,
		regions: selected,
		cpu: edits.cpu ?? defaults?.cpu ?? "",
		memory: edits.memory ?? defaults?.memory ?? "",
		replicas: edits.replicas ?? defaults?.minReplicas ?? 1,
		isPublic: edits.isPublic ?? true,
		subdomain: edits.subdomain ?? slug(name),
		platformDomain: served.find((d) => d === edits.platformDomain) ?? served[0] ?? "",
		port: edits.port ?? String(defaults?.routing?.port ?? ""),
		envText: edits.envText ?? "",
		healthPath: edits.healthPath ?? defaults?.healthCheck?.path ?? "",
	};
	const check = useDomainAvailability(form.isPublic ? form.subdomain : "", form.platformDomain, []);
	const errors = validateForm(form, taken, check.state);
	const shown = (e: string | null, field: keyof NewResourceForm) => (tried || field in edits ? e : null);
	const subdomainError =
		check.state !== "empty" ? null : form.subdomain === "" ? shown(errors.subdomain, "subdomain") : errors.subdomain;
	const set = (patch: Partial<NewResourceForm>) => {
		setEdits((prev) => ({ ...prev, ...patch }));
	};
	const vars = parseEnvText(form.envText).vars;

	const loadError = configQuery.error ?? regionsQuery.error ?? domainsQuery.error;
	const loading = scopes.isLoading || envsLoading || configQuery.isPending || regionsQuery.isPending;
	const canWrite = scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.WRITE);

	const header = (
		<div className="flex flex-col gap-2.5">
			<Link to={workspacePath(orgId, workspaceId)} className="w-fit text-[13px] text-fg3 hover:text-foreground">
				Resources
			</Link>
			<PageHeader title="New resource" />
		</div>
	);

	if (!scopes.isLoading && !canWrite) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="You can't create resources here">
						Creating a resource needs write access to this workspace.
					</EmptyState>
				</Section>
			</Page>
		);
	}
	if (loadError !== null) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="Couldn't load the form">{getErrorMessage(loadError, "Failed to load")}</EmptyState>
				</Section>
			</Page>
		);
	}
	if (!envsLoading && active === undefined) {
		return (
			<Page>
				{header}
				<Section>
					<EmptyState title="No environments yet">Create an environment before adding a resource.</EmptyState>
				</Section>
			</Page>
		);
	}
	if (loading || active === undefined || defaults === undefined) {
		return (
			<Page className="max-w-[1160px]">
				{header}
				<Skeleton className="h-[480px] w-full" />
			</Page>
		);
	}

	return (
		<Page className="max-w-[1160px] gap-[22px]">
			{header}
			<div className="grid grid-cols-1 items-start gap-10 min-[980px]:grid-cols-[minmax(0,1fr)_340px]">
				<fieldset disabled={run !== null} className="m-0 min-w-0 border-0 p-0">
					<FormSection title="Source">
						<Field
							label="Container image"
							hint="A public image. Private registries aren't supported yet."
							error={shown(errors.image, "image")}
						>
							<Input
								value={image}
								className="font-mono"
								placeholder="ghcr.io/acme/web:1.4.2"
								autoComplete="off"
								aria-invalid={shown(errors.image, "image") !== null}
								onChange={(e) => {
									set({ image: e.target.value });
								}}
							/>
						</Field>
						<div className={grid2}>
							<Field label="Name" error={form.name === "" ? shown(errors.name, "name") : errors.name}>
								<Input
									value={name}
									className="font-mono"
									placeholder="web"
									autoComplete="off"
									aria-invalid={form.name === "" ? shown(errors.name, "name") !== null : errors.name !== null}
									onChange={(e) => {
										set({ name: e.target.value });
									}}
								/>
							</Field>
							<div className="flex min-w-0 flex-col gap-1.5">
								<span className="text-[13px] font-medium">Environment</span>
								<ToggleGroup
									variant="segmented"
									aria-label="Environment"
									className="w-fit"
									value={[active.id]}
									onValueChange={(v: string[]) => {
										const next = environments.find((e) => e.id === v[0]);
										if (next !== undefined) setActive(next);
									}}
								>
									{environments.map((e) => (
										<ToggleGroupItem key={e.id} value={e.id} className="font-mono">
											{e.name}
										</ToggleGroupItem>
									))}
								</ToggleGroup>
							</div>
						</div>
					</FormSection>

					<FormSection title="Regions" hint="Loco creates one deployment in each region you pick.">
						{regions.length === 0 ? (
							<span className="text-[13px] text-fg3">No cluster serves {active.name} yet.</span>
						) : (
							<ToggleGroup
								multiple
								variant="outline"
								aria-label="Regions"
								className="flex-wrap"
								value={selected}
								onValueChange={(v: string[]) => {
									setRegionEdit({ envId, regions: v });
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
						{(tried || regionEdit?.envId === envId) && errors.regions !== null && (
							<span className="text-[12.5px] text-bad-fg">{errors.regions}</span>
						)}
					</FormSection>

					<FormSection title="Size and scaling" hint="Each region runs this many replicas of this size.">
						<div className={grid2}>
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
							<div className="flex flex-col gap-1.5">
								<span className="text-[13px] font-medium">Replicas</span>
								<Stepper
									label="Replicas"
									value={form.replicas}
									min={1}
									max={Number.POSITIVE_INFINITY}
									onChange={(replicas) => {
										set({ replicas });
									}}
								/>
							</div>
						</div>
						<SoonRow label="Autoscaling" hint="Scale between a minimum and maximum replica count." />
					</FormSection>

					<FormSection title="Networking">
						<div className="flex items-center justify-between gap-4">
							<span className="flex flex-col gap-0.5">
								<span className="text-[13.5px] font-medium">Public</span>
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
						<div className={grid2}>
							{form.isPublic && (
								<div className="col-span-full flex min-w-0 flex-col gap-1.5">
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
							<Field label="Port" error={shown(errors.port, "port")} className="max-w-[200px]">
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
					</FormSection>

					<FormSection title="Environment variables" hint="Paste a .env file or one KEY=value per line.">
						<Field label="Variables" error={shown(errors.env, "envText")}>
							<Textarea
								rows={5}
								value={form.envText}
								className="font-mono"
								placeholder={"DATABASE_URL=postgres://…\nLOG_LEVEL=info"}
								aria-invalid={shown(errors.env, "envText") !== null}
								onChange={(e) => {
									set({ envText: e.target.value });
								}}
							/>
						</Field>
						{vars.length > 0 && (
							<div className="flex flex-wrap gap-1.5">
								{vars.map(([key]) => (
									<Badge key={key} tone="muted" className="font-mono">
										{key}
									</Badge>
								))}
							</div>
						)}
						<SoonRow label="Secret" hint="Store a variable as a secret instead of plain text." />
					</FormSection>

					<FormSection title="Health check">
						<div className={grid2}>
							<Field
								label="Path"
								error={shown(errors.healthPath, "healthPath")}
								hint="Loco waits for this path to return 2xx before sending traffic. Leave empty for none."
							>
								<Input
									value={form.healthPath}
									className="font-mono"
									placeholder="/healthz"
									aria-invalid={shown(errors.healthPath, "healthPath") !== null}
									onChange={(e) => {
										set({ healthPath: e.target.value });
									}}
								/>
							</Field>
						</div>
					</FormSection>
				</fieldset>

				<SidePanel
					form={form}
					errors={errors}
					env={active}
					defaults={defaults}
					schemaUrl={configQuery.data?.schemaUrl ?? ""}
					orgId={orgId}
					workspaceId={workspaceId}
					run={run}
					onRun={setRun}
					tried={tried}
					onTried={() => {
						setTried(true);
					}}
				/>
			</div>
		</Page>
	);
}
