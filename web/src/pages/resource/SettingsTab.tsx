import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { toast } from "sonner";
import { updateResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { toastConnectError } from "@/lib/error-handler";

import { DomainsSection } from "./DomainsSection";
import { MANAGED_NOTICE_ID, ManagedNotice } from "./ManagedNotice";
import type { Notice, RegionView } from "./model";
import { ScaleSection } from "./ScaleSection";

const NAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function GeneralSection({
	resource,
	managed,
	onSaved,
}: {
	resource: Resource;
	managed: boolean;
	onSaved: () => void;
}) {
	const [name, setName] = useState<string | null>(null);
	const [error, setError] = useState<string | undefined>(undefined);
	const update = useMutation(updateResource);
	const value = name ?? resource.name;
	const dirty = name !== null && name.trim() !== resource.name;

	const save = () => {
		const next = value.trim();
		if (!NAME.test(next)) {
			setError("Lowercase letters, digits and hyphens, up to 63 characters.");
			return;
		}
		setError(undefined);
		update.mutate(
			{ resourceId: resource.id, name: next },
			{
				onSuccess: () => {
					setName(null);
					toast.success(`Renamed to ${next}`);
					onSaved();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to rename resource");
				},
			},
		);
	};

	return (
		<Section title="General">
			<form
				className="grid grid-cols-1 gap-4 px-4 py-3.5 sm:grid-cols-2"
				onSubmit={(e) => {
					e.preventDefault();
					if (dirty) save();
				}}
			>
				<Field label="Name" error={error}>
					<Input
						value={value}
						disabled={managed}
						aria-describedby={managed ? MANAGED_NOTICE_ID : undefined}
						aria-invalid={error !== undefined}
						onChange={(e) => {
							setName(e.target.value);
							setError(undefined);
						}}
					/>
				</Field>
				<Field
					label={
						<span className="flex items-center gap-1.5">
							Description
							<SoonTag />
						</span>
					}
				>
					<Input value={resource.description ?? ""} placeholder="No description" disabled readOnly />
				</Field>
				<div className="flex justify-end gap-2 sm:col-span-2">
					{dirty && (
						<Button
							type="button"
							variant="outline"
							className="h-[30px]"
							onClick={() => {
								setName(null);
								setError(undefined);
							}}
						>
							Reset
						</Button>
					)}
					<Button
						type="submit"
						className="h-[30px]"
						disabled={managed || !dirty || update.isPending}
						aria-describedby={managed ? MANAGED_NOTICE_ID : undefined}
					>
						{update.isPending ? "Saving…" : "Save"}
					</Button>
				</div>
			</form>
		</Section>
	);
}

export function SettingsTab({
	resource,
	environmentId,
	regions,
	onNotice,
	onSaved,
	onDelete,
}: {
	resource: Resource;
	environmentId: string | undefined;
	regions: RegionView[];
	onNotice: (notice: Notice) => void;
	onSaved: () => void;
	onDelete: () => void;
}) {
	const managed = resource.partial !== undefined;
	return (
		<div className="flex max-w-[880px] flex-col gap-5">
			{resource.partial !== undefined && <ManagedNotice />}
			<GeneralSection resource={resource} managed={managed} onSaved={onSaved} />
			<ScaleSection
				resourceId={resource.id}
				resourceName={resource.name}
				environmentId={environmentId}
				regions={regions}
				managed={managed}
				onNotice={onNotice}
				onSaved={onSaved}
			/>
			<DomainsSection domains={resource.domains} managed={managed} onChanged={onSaved} />
			<section className="rounded-lg border border-red bg-background">
				<div className="flex items-center gap-4 px-4 py-3.5">
					<div className="flex min-w-0 flex-1 flex-col gap-0.5">
						<span className="font-semibold text-red">Delete resource</span>
						{managed && (
							<span className="text-sm text-fg3">
								Deleting here causes drift with <code className="font-mono">loco.yaml</code>.
							</span>
						)}
					</div>
					<Button
						variant="destructive-outline"
						className="h-[30px] border-red"
						onClick={onDelete}
					>
						Delete resource
					</Button>
				</div>
			</section>
		</div>
	);
}
