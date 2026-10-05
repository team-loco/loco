import { useState } from "react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";

import { shortId } from "./format";
import { depService, depTag } from "./model";
import { specText } from "./spec";

function TitleWithSub({ title, sub }: { title: string; sub: string }) {
	return (
		<span className="flex items-baseline gap-2.5">
			{title}
			{sub !== "" && <span className="text-sm font-normal text-fg3">{sub}</span>}
		</span>
	);
}

export function SpecDialogContent({ dep, specVersion }: { dep: Deployment; specVersion: number }) {
	const sub = `${shortId(dep.id)} · ${dep.region} · spec v${(dep.specVersion || specVersion).toString()}`;
	return (
		<DialogContent className="w-[640px]" title={<TitleWithSub title="Deployment spec" sub={sub} />}>
			<pre className="m-0 max-h-[70vh] min-h-0 overflow-auto px-4 py-3 font-mono text-sm leading-[1.6] whitespace-pre text-fg2">
				{specText(dep)}
			</pre>
		</DialogContent>
	);
}

interface Change {
	label: string;
	from: string;
	to: string;
}

function rollbackChanges(target: Deployment, current: Deployment | undefined): Change[] {
	if (current === undefined) return [{ label: "Spec", from: "—", to: shortId(target.id) }];
	const t = depService(target);
	const c = depService(current);
	const out: Change[] = [];
	const push = (label: string, from: string, to: string) => {
		if (from !== to) out.push({ label, from, to });
	};
	push("Image", depTag(current), depTag(target));
	push("Replicas", current.replicas.toString(), target.replicas.toString());
	push("CPU", c?.cpu ?? "—", t?.cpu ?? "—");
	push("Memory", c?.memory ?? "—", t?.memory ?? "—");
	const tEnv = t?.env ?? {};
	const cEnv = c?.env ?? {};
	const keys = [...new Set([...Object.keys(cEnv), ...Object.keys(tEnv)])].sort();
	for (const k of keys) push(k, cEnv[k] ?? "—", tEnv[k] ?? "—");
	if (out.length === 0) out.push({ label: "Spec", from: shortId(current.id), to: shortId(target.id) });
	return out;
}

export function RollbackDialogContent({
	resourceName,
	target,
	current,
	pending,
	onConfirm,
}: {
	resourceName: string;
	target: Deployment;
	current: Deployment | undefined;
	pending: boolean;
	onConfirm: () => void;
}) {
	const changes = rollbackChanges(target, current);
	return (
		<DialogContent className="w-[520px]" title={`Roll back ${resourceName}`}>
			<DialogBody className="gap-3 leading-normal text-fg2">
				<span>
					Creates a new deployment in <span className="font-semibold text-foreground">{target.region}</span> from the spec of{" "}
					<span className="font-semibold text-foreground">{shortId(target.id)}</span> ({depTag(target)}).
					{current !== undefined && ` The current deployment ${shortId(current.id)} is superseded once the new pods are ready.`}
				</span>
				<div className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1.5 rounded-sm border border-line bg-bg2 px-3 py-2.5 text-sm">
					{changes.map((c) => (
						<div key={c.label} className="contents">
							<span className="truncate text-fg3">{c.label}</span>
							<span className="min-w-0 break-all">
								<span className="text-bad-fg">{c.from}</span> → <span className="text-ok-fg">{c.to}</span>
							</span>
						</div>
					))}
				</div>
			</DialogBody>
			<DialogFooter>
				<DialogClose render={<Button variant="outline" className="h-[30px]" />}>Cancel</DialogClose>
				<Button className="h-[30px]" disabled={pending} onClick={onConfirm}>
					{pending ? "Rolling back…" : "Roll back"}
				</Button>
			</DialogFooter>
		</DialogContent>
	);
}

export function DeleteDialogContent({
	resource,
	regionNames,
	pending,
	onConfirm,
}: {
	resource: Resource;
	regionNames: string[];
	pending: boolean;
	onConfirm: () => void;
}) {
	const [typed, setTyped] = useState("");
	const ok = typed.trim() === resource.name;
	const deployCount =
		regionNames.length === 1 ? "its active deployment" : `its ${regionNames.length.toString()} active deployments`;
	const regionList = regionNames.length > 0 ? ` in ${regionNames.join(" and ")}` : "";
	const domains = resource.domains;
	const domainText =
		domains.length === 0
			? "no domains"
			: domains.length === 1
				? `the domain ${domains[0]?.domain ?? ""}`
				: `${domains.length.toString()} domains`;
	return (
		<DialogContent className="w-[520px]" title={`Delete ${resource.name}?`}>
			<form
				onSubmit={(e) => {
					e.preventDefault();
					if (ok && !pending) onConfirm();
				}}
			>
				<DialogBody className="gap-3">
					<span className="leading-normal text-fg2">
						Removes the resource, {deployCount}
						{regionList}, and {domainText}. Running pods are terminated immediately.
					</span>
					<label className="flex flex-col gap-1.5">
						<span className="text-sm text-fg3">
							Type <span className="font-semibold text-foreground">{resource.name}</span> to confirm
						</span>
						<Input
							autoFocus
							value={typed}
							placeholder={resource.name}
							className="h-[34px]"
							onChange={(e) => {
								setTyped(e.target.value);
							}}
						/>
					</label>
				</DialogBody>
				<DialogFooter>
					<DialogClose render={<Button type="button" variant="outline" className="h-[30px]" />}>Cancel</DialogClose>
					<Button type="submit" variant="destructive" className="h-[30px]" disabled={!ok || pending}>
						{pending ? "Deleting…" : "Delete resource"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
