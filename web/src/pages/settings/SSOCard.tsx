import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
	configureOrgSSO,
	deleteOrgSSO,
	getOrgSSO,
	setOrgRequireSSO,
} from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { GetOrgSSOResponse, OrgSSO, ServiceProvider } from "@gen/loco/org/v1/org_pb";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Skeleton } from "@/components/design/Skeleton";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useCopy } from "@/hooks/useCopy";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";

import { ConfirmDeleteDialog } from "./ConfirmDeleteDialog";
import { CopyField, SettingsCard } from "./parts";

type MetadataSource = "url" | "xml";

function ServiceProviderFields({ sp }: { sp: ServiceProvider }) {
	const [copied, copy] = useCopy();
	return (
		<div className="grid gap-2 md:grid-cols-2">
			<CopyField
				label="Entity ID / metadata URL"
				value={sp.metadataUrl}
				copied={copied === "metadata"}
				onCopy={() => {
					copy("metadata", sp.metadataUrl);
				}}
			/>
			<CopyField
				label="ACS URL"
				value={sp.acsUrl}
				copied={copied === "acs"}
				onCopy={() => {
					copy("acs", sp.acsUrl);
				}}
			/>
		</div>
	);
}

function ConnectForm({ orgId }: { orgId: string }) {
	const queryClient = useQueryClient();
	const configure = useMutation(configureOrgSSO);
	const [source, setSource] = useState<MetadataSource>("url");
	const [value, setValue] = useState("");

	const submit = (event: FormEvent) => {
		event.preventDefault();
		const metadata = value.trim();
		if (metadata === "") return;
		configure.mutate(
			{
				orgId,
				metadata:
					source === "url" ? { case: "metadataUrl", value: metadata } : { case: "metadataXml", value: metadata },
			},
			{
				onSuccess: () => {
					setValue("");
					toast.success("SSO connected");
					void queryClient.invalidateQueries();
				},
			},
		);
	};

	return (
		<form className="flex flex-col gap-2.5 border-t border-line bg-bg2 px-5 py-3.5" onSubmit={submit}>
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-[12.5px] text-fg3">Identity provider metadata</span>
				<ToggleGroup
					variant="segmented"
					value={[source]}
					onValueChange={(v: string[]) => {
						const next = v[0];
						if (next === "url" || next === "xml") {
							setSource(next);
							configure.reset();
						}
					}}
				>
					<ToggleGroupItem value="url" className="h-7! px-3 text-[12.5px]">
						URL
					</ToggleGroupItem>
					<ToggleGroupItem value="xml" className="h-7! px-3 text-[12.5px]">
						XML
					</ToggleGroupItem>
				</ToggleGroup>
			</div>
			{source === "url" ? (
				<Input
					value={value}
					placeholder="https://idp.example.com/saml/metadata"
					aria-label="Metadata URL"
					className="h-[30px] text-[13.5px]"
					onChange={(e) => {
						setValue(e.target.value);
						configure.reset();
					}}
				/>
			) : (
				<Textarea
					value={value}
					rows={5}
					placeholder='<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" …>'
					aria-label="Metadata XML"
					className="font-mono text-[12.5px] md:text-[12.5px]"
					onChange={(e) => {
						setValue(e.target.value);
						configure.reset();
					}}
				/>
			)}
			{configure.error !== null && (
				<span className="text-sm text-bad-fg">{getErrorMessage(configure.error, "Could not connect SSO")}</span>
			)}
			<div>
				<Button
					type="submit"
					variant="outline"
					className="h-[30px] px-2.5"
					disabled={configure.isPending || value.trim() === ""}
				>
					{configure.isPending ? "Connecting…" : "Connect"}
				</Button>
			</div>
		</form>
	);
}

function Connection({
	orgId,
	orgName,
	sso,
	signedInWithSso,
}: {
	orgId: string;
	orgName: string;
	sso: OrgSSO;
	signedInWithSso: boolean;
}) {
	const queryClient = useQueryClient();
	const setRequire = useMutation(setOrgRequireSSO);
	const remove = useMutation(deleteOrgSSO);
	const [confirming, setConfirming] = useState(false);
	const canRequire = sso.requireSso || signedInWithSso;

	const onRequire = (value: string) => {
		const requireSso = value === "required";
		if (requireSso === sso.requireSso) return;
		setRequire.mutate(
			{ orgId, requireSso },
			{
				onSuccess: () => {
					toast.success(requireSso ? "SSO is now required" : "SSO is now optional");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Could not change the SSO requirement");
				},
			},
		);
	};

	const onRemove = () => {
		remove.mutate(
			{ orgId },
			{
				onSuccess: () => {
					setConfirming(false);
					toast.success("SSO connection removed");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Could not remove the SSO connection");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-3 px-5 py-4">
			<div className="flex flex-wrap items-center gap-2.5">
				<KeyRoundIcon className="size-[15px] shrink-0 text-fg3" />
				<span className="min-w-0 flex-1 truncate font-semibold">SAML connection</span>
				<Badge tone="ok" size="sm">
					Connected
				</Badge>
				<Button
					variant="outline"
					className="h-[30px] px-2.5"
					disabled={remove.isPending}
					onClick={() => {
						setConfirming(true);
					}}
				>
					Remove
				</Button>
			</div>
			<span className="text-[12.5px] text-fg3">
				People on {sso.domains.join(", ")} sign in through your identity provider.
			</span>
			<div className="flex flex-wrap items-center gap-3">
				<span className="text-[12.5px] text-fg3">Members must sign in with SSO</span>
				<ToggleGroup
					variant="segmented"
					value={[sso.requireSso ? "required" : "optional"]}
					disabled={setRequire.isPending}
					onValueChange={(v: string[]) => {
						const next = v[0];
						if (next !== undefined) onRequire(next);
					}}
				>
					<ToggleGroupItem value="optional" className="h-7! px-3 text-[12.5px]">
						Optional
					</ToggleGroupItem>
					<ToggleGroupItem value="required" disabled={!canRequire} className="h-7! px-3 text-[12.5px]">
						Required
					</ToggleGroupItem>
				</ToggleGroup>
			</div>
			<span className="text-[12.5px] text-fg3">
				{canRequire
					? "When required, access to this organization only works from a session that came through this connection, including CLI logins and API tokens created from one."
					: "Sign in through this connection before requiring it, so you keep access."}
			</span>
			<ConfirmDeleteDialog
				open={confirming}
				onOpenChange={setConfirming}
				title="Remove SSO connection"
				text="People will no longer be able to sign in through your identity provider, and SSO will stop being required."
				target={orgName}
				confirmLabel="Remove connection"
				pending={remove.isPending}
				onConfirm={onRemove}
			/>
		</div>
	);
}

function Body({ orgId, orgName, data }: { orgId: string; orgName: string; data: GetOrgSSOResponse }) {
	if (!data.available || data.serviceProvider === undefined) {
		return <div className="px-5 py-4 text-[12.5px] text-fg3">SAML SSO is not enabled on this Loco installation.</div>;
	}
	return (
		<>
			{data.sso !== undefined && (
				<Connection orgId={orgId} orgName={orgName} sso={data.sso} signedInWithSso={data.signedInWithSso} />
			)}
			<div className="flex flex-col gap-2.5 border-t border-line px-5 py-4 first:border-t-0">
				<span className="text-[12.5px] text-fg3">Add Loco to your identity provider as a SAML application with these values.</span>
				<ServiceProviderFields sp={data.serviceProvider} />
			</div>
			{data.sso === undefined && <ConnectForm orgId={orgId} />}
		</>
	);
}

export function SSOCard({ orgId, orgName }: { orgId: string; orgName: string }) {
	const ssoQuery = useQuery(getOrgSSO, { orgId });

	return (
		<SettingsCard>
			<div className="flex flex-col gap-1 border-b border-line px-5 py-3.5">
				<span className="font-semibold">Single sign-on</span>
				<span className="text-[12.5px] text-fg3">
					Connect a SAML identity provider such as Okta, Entra ID or Google Workspace for your verified domains.
				</span>
			</div>
			{ssoQuery.isLoading && (
				<div className="px-5 py-3">
					<Skeleton className="h-9 w-full" />
				</div>
			)}
			{ssoQuery.error !== null && (
				<div className="px-5 py-5 text-fg3">{getErrorMessage(ssoQuery.error, "Failed to load SSO settings")}</div>
			)}
			{ssoQuery.data !== undefined && <Body orgId={orgId} orgName={orgName} data={ssoQuery.data} />}
		</SettingsCard>
	);
}
