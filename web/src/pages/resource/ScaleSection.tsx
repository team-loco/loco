import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { scaleResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { Section } from "@/components/design/Page";
import { getErrorMessage } from "@/lib/error-handler";

import { isQuantity } from "./format";
import { MANAGED_NOTICE_ID } from "./ManagedNotice";
import { depService, type Notice, type RegionView } from "./model";

interface ScaleRow {
	region: string;
	replicas: string;
	cpu: string;
	mem: string;
}

interface Change {
	region: string;
	replicas?: number;
	cpu?: string;
	memory?: string;
}

function baseline(regions: RegionView[]): ScaleRow[] {
	return regions
		.filter((r) => r.current !== undefined)
		.map((r) => {
			const svc = depService(r.current);
			return {
				region: r.name,
				replicas: (r.current?.replicas ?? 0).toString(),
				cpu: svc?.cpu ?? "",
				mem: svc?.memory ?? "",
			};
		});
}

export function ScaleSection({
	resourceId,
	resourceName,
	regions,
	managed,
	onNotice,
	onSaved,
}: {
	resourceId: string;
	resourceName: string;
	regions: RegionView[];
	managed: boolean;
	onNotice: (notice: Notice) => void;
	onSaved: () => void;
}) {
	const base = baseline(regions);
	const [draft, setDraft] = useState<ScaleRow[] | null>(null);
	const [errors, setErrors] = useState<Record<string, string>>({});
	const scale = useMutation(scaleResource);
	const rows = draft ?? base;

	const set = (region: string, patch: Partial<ScaleRow>) => {
		setDraft(rows.map((r) => (r.region === region ? { ...r, ...patch } : r)));
	};

	const apply = async () => {
		const nextErrors: Record<string, string> = {};
		const changes: Change[] = [];
		for (const row of rows) {
			const orig = base.find((b) => b.region === row.region);
			if (orig === undefined) continue;
			const replicas = Number(row.replicas.trim());
			if (!Number.isInteger(replicas) || replicas < 1) nextErrors[`${row.region}.replicas`] = "Whole number, at least 1";
			if (!isQuantity(row.cpu)) nextErrors[`${row.region}.cpu`] = "e.g. 250m or 1";
			if (!isQuantity(row.mem)) nextErrors[`${row.region}.mem`] = "e.g. 512Mi or 1Gi";
			const change: Change = { region: row.region };
			if (row.replicas.trim() !== orig.replicas) change.replicas = replicas;
			if (row.cpu.trim() !== orig.cpu) change.cpu = row.cpu.trim();
			if (row.mem.trim() !== orig.mem) change.memory = row.mem.trim();
			if (change.replicas !== undefined || change.cpu !== undefined || change.memory !== undefined) changes.push(change);
		}
		setErrors(nextErrors);
		if (Object.keys(nextErrors).length > 0 || changes.length === 0) return;
		const failures: string[] = [];
		for (const c of changes) {
			try {
				await scale.mutateAsync({ resourceId, ...c });
			} catch (err) {
				failures.push(`${c.region}: ${getErrorMessage(err, "Failed to scale")}`);
			}
		}
		onSaved();
		setDraft(null);
		if (failures.length > 0) {
			onNotice({ tone: "bad", title: `Scaling ${resourceName} failed`, message: failures.join(" · ") });
			return;
		}
		onNotice({
			tone: "info",
			title: `Scaling ${resourceName}`,
			message: "A new deployment is rolling out with the updated requests.",
		});
	};

	const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(base);
	const describedBy = managed ? MANAGED_NOTICE_ID : undefined;

	return (
		<Section title="Scale">
			{rows.length === 0 && <div className="px-4 py-5 text-fg3">Scaling is available once the resource has a deployment.</div>}
			{rows.map((s) => (
				<div
					key={s.region}
					className="grid grid-cols-1 items-start gap-4 border-b border-line px-4 py-3.5 sm:grid-cols-[130px_repeat(3,minmax(0,1fr))]"
				>
					<span className="pt-[25px] font-semibold">{s.region}</span>
					<Field label="Replicas" error={errors[`${s.region}.replicas`]}>
						<Input
							inputMode="numeric"
							value={s.replicas}
							disabled={managed}
							aria-describedby={describedBy}
							aria-invalid={errors[`${s.region}.replicas`] !== undefined}
							onChange={(e) => {
								set(s.region, { replicas: e.target.value });
							}}
						/>
					</Field>
					<Field label="CPU" error={errors[`${s.region}.cpu`]}>
						<Input
							value={s.cpu}
							disabled={managed}
							aria-describedby={describedBy}
							placeholder="500m"
							aria-invalid={errors[`${s.region}.cpu`] !== undefined}
							onChange={(e) => {
								set(s.region, { cpu: e.target.value });
							}}
						/>
					</Field>
					<Field label="Memory" error={errors[`${s.region}.mem`]}>
						<Input
							value={s.mem}
							disabled={managed}
							aria-describedby={describedBy}
							placeholder="512Mi"
							aria-invalid={errors[`${s.region}.mem`] !== undefined}
							onChange={(e) => {
								set(s.region, { mem: e.target.value });
							}}
						/>
					</Field>
				</div>
			))}
			{rows.length > 0 && (
				<div className="flex justify-end gap-2 px-4 py-3">
					{dirty && (
						<Button
							variant="outline"
							className="h-[30px]"
							onClick={() => {
								setDraft(null);
								setErrors({});
							}}
						>
							Reset
						</Button>
					)}
					<Button
						className="h-[30px]"
						disabled={managed || !dirty || scale.isPending}
						aria-describedby={describedBy}
						onClick={() => void apply()}
					>
						{scale.isPending ? "Applying…" : "Apply"}
					</Button>
				</div>
			)}
		</Section>
	);
}
