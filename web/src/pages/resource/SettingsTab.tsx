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
import type { Notice, RegionView } from "./model";
import { ScaleSection } from "./ScaleSection";

const NAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function GeneralSection({ resource, onSaved }: { resource: Resource; onSaved: () => void }) {
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
					<Button type="submit" className="h-[30px]" disabled={!dirty || update.isPending}>
						{update.isPending ? "Saving…" : "Save"}
					</Button>
				</div>
			</form>
		</Section>
	);
}

export function SettingsTab({
	resource,
	regions,
	onNotice,
	onSaved,
	onDelete,
}: {
	resource: Resource;
	regions: RegionView[];
	onNotice: (notice: Notice) => void;
	onSaved: () => void;
	onDelete: () => void;
}) {
	return (
		<div className="flex max-w-[880px] flex-col gap-5">
			<GeneralSection resource={resource} onSaved={onSaved} />
			<ScaleSection
				resourceId={resource.id}
				resourceName={resource.name}
				regions={regions}
				onNotice={onNotice}
				onSaved={onSaved}
			/>
			<DomainsSection resourceId={resource.id} domains={resource.domains} onChanged={onSaved} />
			<section className="rounded-lg border border-red bg-background">
				<div className="flex items-center gap-4 px-4 py-3.5">
					<span className="flex-1 font-semibold text-red">Delete {resource.name}</span>
					<Button variant="destructive-outline" className="h-[30px] border-red" onClick={onDelete}>
						Delete resource
					</Button>
				</div>
			</section>
		</div>
	);
}
