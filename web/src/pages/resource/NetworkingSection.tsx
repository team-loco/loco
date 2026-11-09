import { useMutation } from "@connectrpc/connect-query";
import { EllipsisIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import {
	checkDomainAvailability,
	deleteResourceDomain,
	setPrimaryResourceDomain,
	updateResourceDomain,
} from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { DomainType, type ResourceDomain } from "@gen/loco/domain/v1/domain_pb";

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
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { SoonTag } from "@/components/design/SoonTag";
import { Switch } from "@/components/design/Switch";
import { toastConnectError } from "@/lib/error-handler";
import { slug } from "@/lib/slug";

import { MANAGED_NOTICE_ID } from "./ManagedNotice";
import { SettingsBlock, type SectionProps } from "./SettingsSections";

const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

function sourceLabel(source: DomainType): string {
	switch (source) {
		case DomainType.PLATFORM_PROVIDED:
			return "platform";
		case DomainType.UNSPECIFIED:
			return "—";
	}
}

function DomainRow({
	domain,
	onlyDomain,
	managed,
	onChanged,
}: {
	domain: ResourceDomain;
	onlyDomain: boolean;
	managed: boolean;
	onChanged: () => void;
}) {
	const [editing, setEditing] = useState<string | null>(null);
	const setPrimary = useMutation(setPrimaryResourceDomain);
	const remove = useMutation(deleteResourceDomain);
	const check = useMutation(checkDomainAvailability);
	const update = useMutation(updateResourceDomain);
	const platform = domain.domainSource === DomainType.PLATFORM_PROVIDED;
	const removeBlocked = domain.isPrimary && !onlyDomain;

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
		<div className="grid h-11 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-line px-4 sm:grid-cols-[minmax(0,1fr)_100px_120px_auto]">
			<a href={`https://${domain.domain}`} target="_blank" rel="noopener noreferrer" className="truncate font-medium">
				{domain.domain}
			</a>
			<span className="hidden text-fg3 sm:inline">{sourceLabel(domain.domainSource)}</span>
			{platform ? (
				<span className="hidden items-center gap-1.5 text-fg2 sm:flex">
					<span className="size-[7px] rounded-full bg-ok-fg" />
					TLS active
				</span>
			) : (
				<span
					className="hidden items-center gap-1.5 text-fg4 sm:flex"
					title="Certificate status for custom domains is not reported yet"
				>
					TLS status
					<SoonTag />
				</span>
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

export function NetworkingSection({
	draft,
	base,
	errors,
	locked,
	describedBy,
	onChange,
	domains,
	served,
	onDomainsChanged,
}: SectionProps & { domains: ResourceDomain[]; served: string[]; onDomainsChanged: () => void }) {
	const sorted = domains.toSorted((a, b) => Number(b.isPrimary) - Number(a.isPrimary));
	const isPublic = draft.isPublic ?? base.isPublic;
	const adding = isPublic && !base.isPublic;
	const platformDomain = draft.platformDomain ?? served[0] ?? "";

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
				<Field label="Domain" error={errors.subdomain ?? errors.platformDomain}>
					<InputGroup>
						<InputGroupInput
							className="font-mono"
							aria-label="Subdomain"
							value={draft.subdomain ?? ""}
							aria-invalid={errors.subdomain !== undefined}
							onChange={(e) => {
								onChange({ subdomain: e.target.value.toLowerCase() });
							}}
						/>
						<InputGroupAddon align="inline-end">
							<InputGroupText>.{platformDomain === "" ? "no platform domain" : platformDomain}</InputGroupText>
						</InputGroupAddon>
					</InputGroup>
				</Field>
			)}
			{isPublic && base.isPublic && (
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">Domains</span>
					<div className="overflow-hidden rounded-[10px] border border-line bg-background">
						{sorted.map((d) => (
							<DomainRow
								key={d.id}
								domain={d}
								onlyDomain={sorted.length === 1}
								managed={locked}
								onChanged={onDomainsChanged}
							/>
						))}
						<div className="flex gap-2 px-4 py-2.5">
							<Input disabled placeholder="shop.example.com" aria-label="Custom domain" className="flex-1" />
							<Button variant="outline" disabled>
								Add custom domain
								<SoonTag />
							</Button>
						</div>
					</div>
					<span className="text-[12.5px] text-fg3">
						Changes to a domain row apply right away and take effect on the next deployment.
					</span>
				</div>
			)}
		</SettingsBlock>
	);
}
