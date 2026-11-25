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
import { CLUSTERS_PATH, clusterPath, systemAccess } from "@/lib/system";

import { JoinToken } from "./JoinToken";
import { refreshFleet, useFleet } from "./shared";

type Target = "existing" | "new" | "local";

const TARGETS = [
	{ value: "existing", label: "Existing region" },
	{ value: "new", label: "New region" },
	{ value: "local", label: "Local" },
] as const;

const LOCAL_REGION = "local-1";
const CODE = /^[a-z0-9]+$/;

export function SystemAddCluster() {
	const fleet = useFleet();
	const queryClient = useQueryClient();
	const create = useMutation(createCluster);
	const [target, setTarget] = useState<Target>("existing");
	const [region, setRegion] = useState("");
	const [country, setCountry] = useState("");
	const [subdivision, setSubdivision] = useState("");
	const [name, setName] = useState("");
	// One request ID per form, so a retry after a lost response recovers the same registration.
	const [requestId] = useState(() => crypto.randomUUID());
	const [created, setCreated] = useState<CreateClusterResponse | null>(null);
	const access = systemAccess(useMyScopes().scopes);
	if (access !== "admin") notFound();

	const newRegionValid = CODE.test(country) && CODE.test(subdivision);
	const ready = target === "existing" ? region !== "" : target === "new" ? newRegionValid : true;

	const submit = async () => {
		const where =
			target === "new"
				? { case: "newRegion" as const, value: { country, subdivision } }
				: { case: "region" as const, value: target === "local" ? LOCAL_REGION : region };
		try {
			setCreated(await create.mutateAsync({ requestId, name: name.trim(), target: where }));
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
								counts={new Map(fleet.regions.map((r) => [r.code, r.clusterCount]))}
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
							<Button type="submit" disabled={!ready || create.isPending}>
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
