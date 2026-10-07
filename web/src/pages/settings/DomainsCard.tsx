import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
	addOrgDomain,
	deleteOrgDomain,
	listOrgDomains,
	setOrgDomainAutoJoin,
	verifyOrgDomain,
} from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgDomain } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { useQueryClient } from "@tanstack/react-query";
import { CheckIcon, CopyIcon, GlobeIcon, Trash2Icon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";

import { SettingsCard } from "./parts";

const AUTO_JOIN_OPTIONS: { value: string; label: string; scope: Scope }[] = [
	{ value: "off", label: "Off", scope: Scope.UNSPECIFIED },
	{ value: "read", label: "Read", scope: Scope.READ },
	{ value: "write", label: "Write", scope: Scope.WRITE },
	{ value: "admin", label: "Admin", scope: Scope.ADMIN },
];

function optionFor(scope: Scope): string {
	return AUTO_JOIN_OPTIONS.find((o) => o.scope === scope)?.value ?? "off";
}

function CopyField({ label, value, copied, onCopy }: { label: string; value: string; copied: boolean; onCopy: () => void }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<span className="text-[12px] text-fg3">{label}</span>
			<div className="flex min-w-0 items-center gap-1.5 rounded-sm border border-line bg-bg2 py-1 pr-1 pl-2.5">
				<code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{value}</code>
				<Button variant="ghost" size="icon-sm" aria-label={`Copy ${label}`} onClick={onCopy}>
					{copied ? <CheckIcon /> : <CopyIcon />}
				</Button>
			</div>
		</div>
	);
}

function DomainRow({ orgId, domain }: { orgId: string; domain: OrgDomain }) {
	const queryClient = useQueryClient();
	const [copied, copy] = useCopy();
	const verify = useMutation(verifyOrgDomain);
	const setAutoJoin = useMutation(setOrgDomainAutoJoin);
	const remove = useMutation(deleteOrgDomain);
	const refresh = async () => {
		await queryClient.invalidateQueries();
	};

	const onVerify = () => {
		verify.mutate(
			{ orgId, domainId: domain.id },
			{
				onSuccess: () => {
					toast.success(`Verified ${domain.domain}`);
					void refresh();
				},
			},
		);
	};

	const onAutoJoin = (value: string) => {
		const option = AUTO_JOIN_OPTIONS.find((o) => o.value === value);
		if (option === undefined) return;
		setAutoJoin.mutate(
			{ orgId, domainId: domain.id, scope: option.scope },
			{
				onSuccess: (res) => {
					const added = res.usersAdded;
					toast.success(
						option.scope === Scope.UNSPECIFIED
							? `Auto-join turned off for ${domain.domain}`
							: added > 0
								? `Auto-join on. Added ${added.toString()} existing ${added === 1 ? "person" : "people"}.`
								: "Auto-join on",
					);
					void refresh();
				},
				onError: (err) => {
					toastConnectError(err, "Could not change auto-join");
				},
			},
		);
	};

	const onRemove = () => {
		remove.mutate(
			{ orgId, domainId: domain.id },
			{
				onSuccess: () => {
					toast.success(`Removed ${domain.domain}`);
					void refresh();
				},
				onError: (err) => {
					toastConnectError(err, "Could not remove the domain");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-3 border-b border-line px-5 py-4 last:border-b-0">
			<div className="flex flex-wrap items-center gap-2.5">
				<GlobeIcon className="size-[15px] shrink-0 text-fg3" />
				<span className="min-w-0 flex-1 truncate font-semibold">{domain.domain}</span>
				{domain.verified ? (
					<Badge tone="ok" size="sm">
						Verified
					</Badge>
				) : (
					<Badge tone="warn" size="sm">
						Pending
					</Badge>
				)}
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={`Remove ${domain.domain}`}
					disabled={remove.isPending}
					onClick={onRemove}
				>
					<Trash2Icon />
				</Button>
			</div>
			{domain.verified ? (
				<div className="flex flex-wrap items-center gap-3">
					<span className="text-[12.5px] text-fg3">Auto-join new people from {domain.domain} as</span>
					<ToggleGroup
						variant="segmented"
						value={[optionFor(domain.autoJoinScope)]}
						disabled={setAutoJoin.isPending}
						onValueChange={(v: string[]) => {
							const next = v[0];
							if (next !== undefined) onAutoJoin(next);
						}}
					>
						{AUTO_JOIN_OPTIONS.map((o) => (
							<ToggleGroupItem key={o.value} value={o.value} className="h-7! px-3 text-[12.5px]">
								{o.label}
							</ToggleGroupItem>
						))}
					</ToggleGroup>
				</div>
			) : (
				<div className="flex flex-col gap-2.5">
					<span className="text-[12.5px] text-fg3">
						Add this TXT record at your DNS provider, then verify. Changes can take a few minutes to appear.
					</span>
					<div className="grid gap-2 md:grid-cols-2">
						<CopyField
							label="Name"
							value={domain.verificationRecordName}
							copied={copied === `${domain.id}:name`}
							onCopy={() => {
								copy(`${domain.id}:name`, domain.verificationRecordName);
							}}
						/>
						<CopyField
							label="Value"
							value={domain.verificationRecordValue}
							copied={copied === `${domain.id}:value`}
							onCopy={() => {
								copy(`${domain.id}:value`, domain.verificationRecordValue);
							}}
						/>
					</div>
					{verify.error !== null && (
						<span className="text-sm text-bad-fg">{getErrorMessage(verify.error, "Verification failed")}</span>
					)}
					<div>
						<Button variant="outline" className="h-[30px] px-2.5" disabled={verify.isPending} onClick={onVerify}>
							{verify.isPending ? "Checking…" : "Verify"}
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}

export function DomainsCard({ orgId }: { orgId: string }) {
	const queryClient = useQueryClient();
	const domainsQuery = useQuery(listOrgDomains, { orgId });
	const add = useMutation(addOrgDomain);
	const [draft, setDraft] = useState("");
	const domains = domainsQuery.data?.domains ?? [];

	const submit = (event: FormEvent) => {
		event.preventDefault();
		const domain = draft.trim();
		if (domain === "") return;
		add.mutate(
			{ orgId, domain },
			{
				onSuccess: () => {
					setDraft("");
					void queryClient.invalidateQueries();
				},
			},
		);
	};

	return (
		<SettingsCard>
			<div className="flex flex-col gap-1 border-b border-line px-5 py-3.5">
				<span className="font-semibold">Domains</span>
				<span className="text-[12.5px] text-fg3">
					Verify the email domains your organization owns. People who sign in with a verified address on a domain can
					join automatically.
				</span>
			</div>
			{domainsQuery.isLoading && (
				<div className="px-5 py-3">
					<Skeleton className="h-9 w-full" />
				</div>
			)}
			{domainsQuery.error !== null && (
				<div className="px-5 py-5 text-fg3">{getErrorMessage(domainsQuery.error, "Failed to load domains")}</div>
			)}
			{domains.map((d) => (
				<DomainRow key={d.id} orgId={orgId} domain={d} />
			))}
			<form className="flex flex-col gap-1.5 border-t border-line bg-bg2 px-5 py-3.5" onSubmit={submit}>
				<div className="flex flex-wrap items-center gap-2">
					<Input
						value={draft}
						placeholder="example.com"
						aria-label="Domain"
						className="h-[30px] max-w-[280px] flex-1 text-[13.5px]"
						onChange={(e) => {
							setDraft(e.target.value);
							add.reset();
						}}
					/>
					<Button type="submit" variant="outline" className="h-[30px] px-2.5" disabled={add.isPending || draft.trim() === ""}>
						Add domain
					</Button>
				</div>
				{add.error !== null && <span className="text-sm text-bad-fg">{getErrorMessage(add.error, "Could not add the domain")}</span>}
			</form>
		</SettingsCard>
	);
}
