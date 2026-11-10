import { ArrowRightIcon } from "lucide-react";
import { useRef, useState } from "react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { cn } from "@/lib/utils";

import { shortId } from "./format";
import { depTag } from "./model";
import { specChanges } from "./spec";

function DiffCell({ value, tone }: { value: string | null; tone: "add" | "del" }) {
	const cls = value === null ? "bg-hatch" : tone === "add" ? "bg-ok-bg text-ok-fg" : "bg-bad-bg text-bad-fg";
	return (
		<td className={cn("border-l border-line px-2.5 py-0.5 align-top break-all", cls)}>
			{value ?? <span className="sr-only">not set</span>}
		</td>
	);
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
	const changes = current === undefined ? [] : specChanges(current, target);
	return (
		<DialogContent size="wide" title={`Roll back ${resourceName} in ${target.region}`}>
			<DialogBody className="gap-3.5 leading-normal text-fg2">
				{changes.length === 0 && (
					<div className="flex items-center gap-3.5 rounded-lg bg-panel px-3.5 py-3">
						<div className="flex min-w-0 flex-col gap-0.5 text-[13px]">
							<span className="text-[12px] text-fg3">Now running</span>
							<span className="truncate font-mono text-foreground">
								{current === undefined ? "—" : `${depTag(current)} · ${shortId(current.id)}`}
							</span>
						</div>
						<ArrowRightIcon aria-hidden className="size-4 shrink-0 text-fg3" />
						<div className="flex min-w-0 flex-col gap-0.5 text-[13px]">
							<span className="text-[12px] text-fg3">Will run</span>
							<span className="truncate font-mono text-foreground">
								{depTag(target)} · {shortId(target.id)}
							</span>
						</div>
					</div>
				)}
				{current !== undefined && changes.length === 0 && (
					<span className="text-[12.5px] text-fg3">Same spec as the running deployment</span>
				)}
				{current !== undefined && changes.length > 0 && (
					<div className="flex flex-col overflow-hidden rounded-lg bg-panel">
						<span className="border-b border-line px-3.5 py-2.5 text-[12.5px] text-fg3">
							{`${changes.length.toString()} ${changes.length === 1 ? "change" : "changes"}`}
						</span>
						<div className="max-h-[min(52vh,420px)] overflow-y-auto">
							<table className="w-full table-fixed border-collapse font-mono text-[12px] leading-[1.6]">
								<colgroup>
									<col className="w-24 sm:w-[190px]" />
									<col />
									<col />
								</colgroup>
								<thead className="sticky top-0 bg-panel align-bottom font-sans text-[11.5px] text-fg3">
									<tr className="border-b border-line">
										<th scope="col" className="py-2 pl-3.5 text-left font-normal">
											<span className="sr-only">Setting</span>
										</th>
										<th scope="col" className="px-2.5 py-2 text-left font-normal">
											Now running <span className="font-mono text-foreground">{depTag(current)}</span>
										</th>
										<th scope="col" className="px-2.5 py-2 text-left font-normal">
											Will run <span className="font-mono text-foreground">{depTag(target)}</span>
										</th>
									</tr>
								</thead>
								<tbody>
									{changes.map((c) => (
										<tr key={c.label}>
											<th scope="row" className="py-0.5 pl-3.5 text-left align-top font-normal break-all text-fg3">
												{c.label}
											</th>
											<DiffCell value={c.from} tone="del" />
											<DiffCell value={c.to} tone="add" />
										</tr>
									))}
								</tbody>
							</table>
						</div>
					</div>
				)}
				<span>
					Creates a new deployment in <span className="font-mono text-foreground">{target.region}</span> from the spec
					of <span className="font-mono text-foreground">{shortId(target.id)}</span>.
					{current !== undefined && " The running deployment is superseded once the new replicas are ready."}
				</span>
			</DialogBody>
			<DialogFooter>
				<DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
				<Button disabled={pending} onClick={onConfirm}>
					{pending ? "Rolling back…" : "Roll back"}
				</Button>
			</DialogFooter>
		</DialogContent>
	);
}

export function DeleteDialogContent({
	resource,
	places,
	pending,
	onConfirm,
}: {
	resource: Resource;
	places: string[];
	pending: boolean;
	onConfirm: () => void;
}) {
	const [typed, setTyped] = useState("");
	const cancelRef = useRef<HTMLButtonElement>(null);
	const ok = typed.trim() === resource.name;
	const deployCount =
		places.length === 1 ? "its active deployment" : `its ${places.length.toString()} active deployments`;
	const placeList = places.length > 0 ? ` in ${places.join(" and ")}` : "";
	const domains = resource.domains;
	const domainText =
		domains.length === 0
			? "no domains"
			: domains.length === 1
				? `the domain ${domains[0]?.domain ?? ""}`
				: `${domains.length.toString()} domains`;
	return (
		<DialogContent className="w-[520px]" title="Delete resource?" initialFocus={cancelRef}>
			<form
				noValidate
				onSubmit={(e) => {
					e.preventDefault();
					if (ok && !pending) onConfirm();
				}}
			>
				<DialogBody className="gap-3">
					<code className="w-fit max-w-full rounded-sm bg-panel px-2 py-1 font-mono font-semibold break-all">
						{resource.name}
					</code>
					<span className="leading-normal text-fg2">
						Removes the resource, {deployCount}
						{placeList}, and {domainText}. Running replicas stop immediately.
					</span>
					{resource.partial !== undefined && (
						<p className="rounded-md border border-warn/30 bg-warn-bg px-3 py-2 text-sm text-warn-fg">
							Deleting here causes drift with <code className="font-mono">loco.yaml</code>. Applying the file again can
							recreate this resource. Remove it from the file to keep it deleted.
						</p>
					)}
					<label className="flex flex-col gap-1.5">
						<span className="text-sm text-fg3">
							Type <span className="font-mono font-semibold text-foreground">{resource.name}</span> to confirm
						</span>
						<Input
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
					<DialogClose render={<Button ref={cancelRef} type="button" variant="outline" className="h-[30px]" />}>
						Cancel
					</DialogClose>
					<Button type="submit" variant="destructive" className="h-[30px]" disabled={!ok || pending}>
						{pending ? "Deleting…" : "Delete resource"}
					</Button>
				</DialogFooter>
			</form>
		</DialogContent>
	);
}
