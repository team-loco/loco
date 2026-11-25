import { useMutation } from "@connectrpc/connect-query";
import { createCluster } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import type { CreateClusterResponse } from "@gen/loco/cluster/v1/cluster_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { PickFilter } from "@/components/design/FilterPopover";
import { Input } from "@/components/design/Input";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { SegmentedChoice } from "@/components/design/SegmentedChoice";
import { ServerIcon } from "lucide-react";
import { useMyScopes } from "@/hooks/useMyScopes";
import { toastConnectError } from "@/lib/error-handler";
import { notFound } from "@/lib/not-found";
import {
	CLUSTERS_PATH,
	clusterPath,
	LOCAL_REGION,
	registrationTarget,
	systemAccess,
	type TargetKind,
} from "@/lib/system";

import { JoinToken } from "./JoinToken";
import { refreshFleet, useFleet } from "./shared";

const TARGETS = [
	{ value: "existing", label: "Existing region" },
	{ value: "new", label: "New region" },
	{ value: "local", label: "Local" },
] as const;

export function SystemAddCluster() {
	const fleet = useFleet();
	const queryClient = useQueryClient();
	const create = useMutation(createCluster);
	const [target, setTarget] = useState<TargetKind>("existing");
	const [region, setRegion] = useState("");
	const [country, setCountry] = useState("");
	const [subdivision, setSubdivision] = useState("");
	const [name, setName] = useState("");
	// The request ID belongs to the submitted registration: a retry of it reuses the ID, an edited one gets a new ID.
	const [attempt, setAttempt] = useState({ key: "", requestId: "" });
	const [created, setCreated] = useState<CreateClusterResponse | null>(null);
	const access = systemAccess(useMyScopes().scopes);
	if (access !== "admin") notFound();

	const registration = registrationTarget(target, region, country, subdivision);

	const submit = async () => {
		if (registration === undefined) return;
		const key = JSON.stringify([registration, name.trim()]);
		const requestId = attempt.key === key ? attempt.requestId : crypto.randomUUID();
		setAttempt({ key, requestId });
		try {
			setCreated(await create.mutateAsync({ requestId, name: name.trim(), target: registration }));
			await refreshFleet(queryClient);
		} catch (err) {
			toastConnectError(err, "Failed to create the cluster");
		}
	};

	return (
		<Page>
			<PageHeader title="Add cluster" />
			{created === null ? (
				<Section title="Details">
					<form
						className="flex max-w-[520px] flex-col gap-4 p-5"
						onSubmit={(event) => {
							event.preventDefault();
							void submit();
						}}
					>
						<Field label="Region">
							<SegmentedChoice
								label="Region"
								options={TARGETS}
								value={target}
								onChange={(next) => {
									setTarget(TARGETS.find((t) => t.value === next)?.value ?? "existing");
								}}
							/>
						</Field>
						{target === "existing" && (
							<PickFilter
								icon={<ServerIcon />}
								label="Choose a region"
								items={fleet.regions.map((r) => ({ id: r.code, name: r.code }))}
								counts={new Map(fleet.regions.map((r) => [r.code, r.count]))}
								value={region}
								clearable={false}
								onChange={setRegion}
							/>
						)}
						{target === "new" && (
							<div className="grid grid-cols-2 gap-3">
								<Field label="Country" hint="Lowercase letters and digits, such as us.">
									<Input
										value={country}
										placeholder="us"
										onChange={(e) => {
											setCountry(e.target.value.trim());
										}}
									/>
								</Field>
								<Field label="State or province" hint="Such as ca.">
									<Input
										value={subdivision}
										placeholder="ca"
										onChange={(e) => {
											setSubdivision(e.target.value.trim());
										}}
									/>
								</Field>
							</div>
						)}
						{target === "local" && (
							<p className="m-0 text-[13px] text-fg3">
								Local clusters join the development region <span className="font-mono">{LOCAL_REGION}</span>.
							</p>
						)}
						<Field label="Name" hint="Leave empty to name the first cluster of a region after its code.">
							<Input
								value={name}
								maxLength={255}
								onChange={(e) => {
									setName(e.target.value);
								}}
							/>
						</Field>
						<div className="flex gap-2">
							<Button type="submit" disabled={registration === undefined || create.isPending}>
								{create.isPending ? "Creating..." : "Create cluster"}
							</Button>
							<Button variant="outline" nativeButton={false} render={<Link to={CLUSTERS_PATH} />}>
								Cancel
							</Button>
						</div>
					</form>
				</Section>
			) : (
				<Section title={`Join ${created.name}`}>
					<div className="flex max-w-[720px] flex-col gap-4 p-5">
						<p className="m-0 text-[13.5px] text-fg2">
							The cluster is registered in <span className="font-mono">{created.region}</span>. It shows Never reported
							until its agent connects with the token.
						</p>
						{created.joinToken !== undefined && (
							<JoinToken
								region={created.region}
								token={created.joinToken.token}
								expiresAt={created.joinToken.expiresAt}
							/>
						)}
						<div className="flex gap-2">
							<Button nativeButton={false} render={<Link to={clusterPath(created.clusterId)} />}>
								Open {created.name}
							</Button>
							<Button variant="outline" nativeButton={false} render={<Link to={CLUSTERS_PATH} />}>
								Back to clusters
							</Button>
						</div>
					</div>
				</Section>
			)}
		</Page>
	);
}
