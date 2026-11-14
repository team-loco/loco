import { useMutation } from "@connectrpc/connect-query";
import { EllipsisIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import {
	checkDomainAvailability,
	createResourceDomain,
	deleteResourceDomain,
	setPrimaryResourceDomain,
	updateResourceDomain,
} from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";
import { DomainType, type PlatformDomain, type ResourceDomain } from "@gen/loco/domain/v1/domain_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { PlatformDomainInput } from "@/components/design/PlatformDomainInput";
import { Switch } from "@/components/design/Switch";
import { useDomainAvailability } from "@/hooks/useDomainAvailability";
import { toastConnectError } from "@/lib/error-handler";
import { domainCoverage } from "@/lib/platform-domain";
import { slug } from "@/lib/slug";
import { LimitedAction, LimitUsageText } from "@/components/design/LimitedAction";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { type LimitUsage, limitReason, limitUsage } from "@/lib/limits";

import { MANAGED_NOTICE_ID } from "./ManagedNotice";
import { SettingsBlock, type SectionProps } from "./SettingsSections";

const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

function DomainRow({
	domain,
	served,
	running,
	onlyDomain,
	managed,
	onChanged,
}: {
	domain: ResourceDomain;
	served: readonly string[];
	running: readonly string[];
	onlyDomain: boolean;
	managed: boolean;
	onChanged: () => void;
}) {
	const [editing, setEditing] = useState<string | null>(null);
	const setPrimary = useMutation(setPrimaryResourceDomain);
	const remove = useMutation(deleteResourceDomain);
	const check = useMutation(checkDomainAvailability);
	const update = useMutation(updateResourceDomain);
	const removeBlocked = domain.isPrimary && !onlyDomain;
	const { serving, missing } = domainCoverage(served, running);

	const saveEdit = async () => {
		if (editing === null) return;
		const host = editing.trim().toLowerCase();
		if (host === domain.domain) {
			setEditing(null);
			return;
		}
		if (!HOST.test(host)) {
			toast.error("Enter a valid hostname, like app.example.com.");
			return;
		}
		try {
			const res = await check.mutateAsync({ domain: host });
			if (!res.isAvailable) {
				toast.error(`${host} is already in use.`);
				return;
			}
			await update.mutateAsync({ domainId: domain.id, domain: host });
			setEditing(null);
			onChanged();
		} catch (err) {
			toastConnectError(err, "Failed to update domain");
		}
	};

	if (editing !== null) {
		return (
			<div className="flex items-center gap-2 border-b border-line px-4 py-1.5">
				<Input
					autoFocus
					value={editing}
					onChange={(e) => {
						setEditing(e.target.value);
					}}
					onKeyDown={(e) => {
						if (e.key === "Enter") void saveEdit();
						if (e.key === "Escape") setEditing(null);
					}}
					className="flex-1"
				/>
				<Button
					variant="outline"
					onClick={() => {
						setEditing(null);
					}}
				>
					Cancel
				</Button>
				<Button disabled={update.isPending || check.isPending} onClick={() => void saveEdit()}>
					Save
				</Button>
			</div>
		);
	}

	return (
		<div className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line px-4 py-2">
			<a
				href={`https://${domain.domain}`}
				target="_blank"
				rel="noopener noreferrer"
				className="w-full font-mono text-[12.5px] font-medium [overflow-wrap:anywhere] sm:w-auto sm:min-w-0 sm:flex-1"
			>
				{domain.domain}
			</a>
			<span className="font-mono text-[11.5px] whitespace-nowrap text-fg3">
				{serving.length === 0 ? "no regions" : serving.join(", ")}
			</span>
			{missing.length > 0 && (
				<span className="text-[11.5px] whitespace-nowrap text-warn-fg">not served in {missing.join(", ")}</span>
			)}
			<div className="flex items-center justify-end gap-1.5">
				{domain.isPrimary && <Badge tone="pill">primary</Badge>}
				<DropdownMenu>
					<DropdownMenuTrigger
						disabled={managed}
						render={
							<Button
								variant="ghost"
								size="icon-sm"
								className="text-fg3"
								aria-label={`Actions for ${domain.domain}`}
								aria-describedby={managed ? MANAGED_NOTICE_ID : undefined}
							/>
						}
					>
						<EllipsisIcon />
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="w-auto min-w-[180px]">
						<DropdownMenuItem
							disabled={domain.isPrimary || setPrimary.isPending}
							onClick={() => {
								setPrimary.mutate(
									{ resourceId: domain.resourceId, domainId: domain.id },
									{
										onSuccess: onChanged,
										onError: (err) => {
											toastConnectError(err, "Failed to set primary domain");
										},
									},
								);
							}}
						>
							Set as primary
						</DropdownMenuItem>
						<DropdownMenuItem
							onClick={() => {
								setEditing(domain.domain);
							}}
						>
							Edit hostname
						</DropdownMenuItem>
						<DropdownMenuItem
							variant="destructive"
							disabled={removeBlocked || remove.isPending}
							title={removeBlocked ? "Make another domain primary first" : undefined}
							onClick={() => {
								remove.mutate(
									{ domainId: domain.id },
									{
										onSuccess: () => {
											toast.success(
												onlyDomain
													? `Removed ${domain.domain}. The service becomes private on its next deployment.`
													: `Removed ${domain.domain}`,
											);
											onChanged();
										},
										onError: (err) => {
											toastConnectError(err, "Failed to remove domain");
										},
									},
								);
							}}
						>
							Remove
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
		</div>
	);
}

function AddDomainForm({
	resourceId,
	resourceName,
	own,
	usage,
	options,
	regions,
	running,
	locked,
	describedBy,
	onAdded,
}: {
	resourceId: string;
	resourceName: string;
	own: readonly string[];
	usage: LimitUsage | null;
	options: PlatformDomain[];
	regions: readonly RegionInfo[];
	running: readonly string[];
	locked: boolean;
	describedBy: string | undefined;
	onAdded: () => void;
}) {
	const [subdomain, setSubdomain] = useState("");
	const [picked, setPicked] = useState<string | null>(null);
	const create = useMutation(createResourceDomain);
	const blocked = limitReason(usage);
	const served = options.filter((d) => domainCoverage(d.regions, running).missing.length === 0);
	const platformDomain = (served.find((d) => d.domain === picked) ?? served[0])?.domain ?? "";
	const check = useDomainAvailability(subdomain, platformDomain, own);
	const unservedId = useId();
	const unserved = served.length === 0;

	return (
		<form
			className="flex flex-wrap items-start gap-2 px-4 py-2.5"
			onSubmit={(e) => {
				e.preventDefault();
				if (check.state !== "available") return;
				create.mutate(
					{
						resourceId,
						domain: { domainSource: DomainType.PLATFORM_PROVIDED, subdomain, platformDomain },
					},
					{
						onSuccess: () => {
							setSubdomain("");
							onAdded();
						},
						onError: (err) => {
							toastConnectError(err, "Failed to add domain");
						},
					},
				);
			}}
		>
			<PlatformDomainInput
				className="max-w-[460px] min-w-[240px] flex-1"
				subdomain={subdomain}
				platformDomain={platformDomain}
				options={served}
				regions={regions}
				check={check}
				owner={resourceName}
				placeholder={slug(resourceName)}
				disabled={locked}
				describedBy={[unserved ? unservedId : undefined, describedBy].filter(Boolean).join(" ") || undefined}
				onSubdomainChange={setSubdomain}
				onPlatformDomainChange={setPicked}
			/>
			<LimitedAction usage={usage} reason={blocked}>
				<Button
					type="submit"
					variant="outline"
					disabled={locked || check.state !== "available" || create.isPending || blocked !== null}
				>
					Add
				</Button>
			</LimitedAction>
			{unserved && (
				<span id={unservedId} className="w-full text-[12.5px] text-fg3">
					No platform domain is served in every region of {resourceName}
				</span>
			)}
		</form>
	);
}

export function NetworkingSection({
	draft,
	base,
	errors,
	locked,
	describedBy,
	onChange,
	resourceId,
	domains,
	platformDomains,
	regions,
	served,
	onDomainsChanged,
}: SectionProps & {
	resourceId: string;
	domains: ResourceDomain[];
	platformDomains: PlatformDomain[];
	regions: readonly RegionInfo[];
	served: string[];
	onDomainsChanged: () => void;
}) {
	const { activeOrgId } = useOrgWorkspace();
	const limits = useOrgLimits(activeOrgId ?? "");
	const usage = limitUsage(domains.length, limits?.domainsPerResource, "domains", "resource");
	const sorted = domains.toSorted((a, b) => Number(b.isPrimary) - Number(a.isPrimary));
	const own = domains.map((d) => d.domain);
	const running = Object.keys(base.regions).filter((r) => base.regions[r] === true);
	const isPublic = draft.isPublic ?? base.isPublic;
	const adding = isPublic && !base.isPublic;
	const platformDomain = draft.platformDomain ?? served[0] ?? "";
	const subdomain = draft.subdomain ?? "";
	const check = useDomainAvailability(adding ? subdomain : "", platformDomain, own);
	const draftError = check.state === "empty" ? (errors.subdomain ?? errors.platformDomain) : undefined;

	return (
		<SettingsBlock id="set-net" title="Networking">
			<div className="flex items-center justify-between gap-4">
				<span className="flex flex-col gap-0.5">
					<span className="text-[13.5px] font-medium">Public</span>
					<span className="text-[12.5px] text-fg3">
						{base.isPublic && !isPublic
							? "Saving removes every domain and redeploys, so the resource only answers inside the workspace."
							: "Serve this resource on the internet over HTTPS."}
					</span>
				</span>
				<Switch
					checked={isPublic}
					disabled={locked}
					aria-label="Public"
					aria-describedby={describedBy}
					onCheckedChange={(checked: boolean) => {
						onChange({ isPublic: checked, platformDomain, subdomain: draft.subdomain ?? slug(base.name) });
					}}
				/>
			</div>
			<Field label="Port" error={errors.port} hint="The port the container listens on.">
				<Input
					className="w-40 font-mono"
					inputMode="numeric"
					value={draft.port ?? base.port}
					disabled={locked}
					aria-describedby={describedBy}
					aria-invalid={errors.port !== undefined}
					onChange={(e) => {
						onChange({ port: e.target.value });
					}}
				/>
			</Field>
			{adding && (
				<div className="flex min-w-0 flex-col gap-1.5">
					<span className="text-[13px] font-medium">Domain</span>
					<PlatformDomainInput
						className="max-w-[460px]"
						subdomain={subdomain}
						platformDomain={platformDomain}
						options={platformDomains.filter((d) => served.includes(d.domain))}
						regions={regions}
						check={check}
						owner={base.name}
						onSubdomainChange={(next) => {
							onChange({ subdomain: next });
						}}
						onPlatformDomainChange={(next) => {
							onChange({ platformDomain: next });
						}}
					/>
					{draftError !== undefined && <span className="text-[12.5px] text-bad-fg">{draftError}</span>}
				</div>
			)}
			{isPublic && base.isPublic && (
				<div className="flex flex-col gap-1.5">
					<span className="flex items-baseline gap-2">
						<span className="text-[13px] font-medium">Domains</span>
						<LimitUsageText usage={usage} />
					</span>
					<div className="overflow-hidden rounded-card border border-line bg-background">
						{sorted.map((d) => (
							<DomainRow
								key={d.id}
								domain={d}
								served={platformDomains.find((p) => p.domain === d.platformDomain)?.regions ?? []}
								running={running}
								onlyDomain={sorted.length === 1}
								managed={locked}
								onChanged={onDomainsChanged}
							/>
						))}
						{sorted.length === 0 && (
							<div className="border-b border-line px-4 py-3.5 text-[13px] text-fg3">No domains yet.</div>
						)}
						<AddDomainForm
							resourceId={resourceId}
							resourceName={base.name}
							own={own}
							usage={usage}
							options={platformDomains}
							regions={regions}
							running={running}
							locked={locked}
							describedBy={describedBy}
							onAdded={onDomainsChanged}
						/>
					</div>
					<span className="text-[12.5px] text-fg3">
						Changes to a domain row apply right away and take effect on the next deployment.
					</span>
				</div>
			)}
		</SettingsBlock>
	);
}
