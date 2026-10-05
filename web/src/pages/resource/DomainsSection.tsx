import { useMutation } from "@connectrpc/connect-query";
import { EllipsisIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import {
	checkDomainAvailability,
	createResourceDomain,
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
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { toastConnectError } from "@/lib/error-handler";

const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/;

function sourceLabel(source: DomainType): string {
	switch (source) {
		case DomainType.PLATFORM_PROVIDED:
			return "platform";
		case DomainType.USER_PROVIDED:
			return "custom";
		case DomainType.UNSPECIFIED:
			return "—";
	}
}

function DomainRow({ domain, onChanged }: { domain: ResourceDomain; onChanged: () => void }) {
	const [editing, setEditing] = useState<string | null>(null);
	const setPrimary = useMutation(setPrimaryResourceDomain);
	const remove = useMutation(deleteResourceDomain);
	const check = useMutation(checkDomainAvailability);
	const update = useMutation(updateResourceDomain);
	const platform = domain.domainSource === DomainType.PLATFORM_PROVIDED;

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
		<div className="grid h-11 grid-cols-[minmax(0,1fr)_100px_120px_auto] items-center gap-3 border-b border-line px-4">
			<a href={`https://${domain.domain}`} target="_blank" rel="noopener noreferrer" className="truncate font-medium">
				{domain.domain}
			</a>
			<span className="text-fg3">{sourceLabel(domain.domainSource)}</span>
			{platform ? (
				<span className="flex items-center gap-1.5 text-fg2">
					<span className="size-[7px] rounded-full bg-[#16a34a]" />
					TLS active
				</span>
			) : (
				<span className="flex items-center gap-1.5 text-fg4" title="Certificate status for custom domains is not reported yet">
					TLS status
					<SoonTag />
				</span>
			)}
			<div className="flex items-center justify-end gap-1.5">
				{domain.isPrimary && (
					<Badge tone="outline" size="sm" className="text-fg3">
						primary
					</Badge>
				)}
				<DropdownMenu>
					<DropdownMenuTrigger
						render={<Button variant="ghost" size="icon-sm" className="text-fg3" aria-label={`Actions for ${domain.domain}`} />}
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
							disabled={domain.isPrimary || remove.isPending}
							title={domain.isPrimary ? "Make another domain primary first" : undefined}
							onClick={() => {
								remove.mutate(
									{ domainId: domain.id },
									{
										onSuccess: () => {
											toast.success(`Removed ${domain.domain}`);
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

export function DomainsSection({
	resourceId,
	domains,
	onChanged,
}: {
	resourceId: string;
	domains: ResourceDomain[];
	onChanged: () => void;
}) {
	const [host, setHost] = useState("");
	const [error, setError] = useState<string | undefined>(undefined);
	const add = useMutation(createResourceDomain);
	const sorted = [...domains].sort((a, b) => Number(b.isPrimary) - Number(a.isPrimary));

	const submit = () => {
		const value = host.trim().toLowerCase();
		if (!HOST.test(value)) {
			setError("Enter a valid hostname, like app.example.com.");
			return;
		}
		setError(undefined);
		add.mutate(
			{ resourceId, domain: { domainSource: DomainType.USER_PROVIDED, domain: value } },
			{
				onSuccess: () => {
					setHost("");
					toast.success(`Added ${value}`);
					onChanged();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to add domain");
				},
			},
		);
	};

	return (
		<Section title="Domains">
			{sorted.length === 0 && (
				<div className="border-b border-line px-4 py-4 text-fg3">
					No domains. The resource receives no internet traffic until one is added.
				</div>
			)}
			{sorted.map((d) => (
				<DomainRow key={d.id} domain={d} onChanged={onChanged} />
			))}
			<form
				className="flex flex-col gap-1.5 px-4 py-3"
				onSubmit={(e) => {
					e.preventDefault();
					submit();
				}}
			>
				<div className="flex gap-2">
					<Input
						value={host}
						placeholder="app.example.com"
						aria-invalid={error !== undefined}
						aria-label="Custom domain"
						onChange={(e) => {
							setHost(e.target.value);
							setError(undefined);
						}}
						className="flex-1"
					/>
					<Button type="submit" variant="outline" disabled={host.trim() === "" || add.isPending}>
						{add.isPending ? "Adding…" : "Add custom domain"}
					</Button>
				</div>
				{error !== undefined && <span className="text-sm text-bad-fg">{error}</span>}
			</form>
		</Section>
	);
}
