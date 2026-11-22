import { Fragment } from "react";
import { InfoIcon, OctagonAlertIcon, RotateCwIcon, TriangleAlertIcon, Undo2Icon, XIcon } from "lucide-react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Banner } from "@/components/design/Banner";
import { Button } from "@/components/design/Button";
import { Notice as InfoNotice } from "@/components/design/Notice";
import { NEEDS_WRITE } from "@/hooks/useMyScopes";
import { cn, formatShortId } from "@/lib/utils";

import { servingBehindStop } from "./deployment-progress";
import { canRollBackTo, depTag, type Notice, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";

function toneClass(tone: Notice["tone"]): string {
	switch (tone) {
		case "info":
			return "border-info-fg bg-info-bg text-info-fg";
		case "warn":
			return "border-warn-fg bg-warn-bg text-warn-fg";
		case "bad":
			return "border-bad-fg bg-bad-bg text-bad-fg";
	}
}

function ToneIcon({ tone }: { tone: Notice["tone"] }) {
	switch (tone) {
		case "info":
			return <InfoIcon className="size-[18px]" />;
		case "warn":
			return <TriangleAlertIcon className="size-[18px]" />;
		case "bad":
			return <OctagonAlertIcon className="size-[18px]" />;
	}
}

export function NoticeBanner({ notice, onDismiss }: { notice: Notice; onDismiss: () => void }) {
	return (
		<div
			role="status"
			className={cn(
				"grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-lg border px-3.5 py-2.5",
				toneClass(notice.tone),
			)}
		>
			<span className="flex">
				<ToneIcon tone={notice.tone} />
			</span>
			<div className="flex min-w-0 flex-col gap-px">
				<span className="font-semibold">{notice.title}</span>
				<span className="break-words whitespace-pre-line">{notice.message}</span>
			</div>
			<button
				type="button"
				aria-label="Dismiss"
				onClick={onDismiss}
				className="flex size-[26px] items-center justify-center rounded-sm border-0 bg-transparent text-inherit hover:bg-foreground/5"
			>
				<XIcon className="size-4" />
			</button>
		</div>
	);
}

export function ServingBanner({
	regions,
	canWrite,
	onRollback,
}: {
	regions: RegionView[];
	canWrite: boolean;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const found = servingBehindStop(regions);
	if (found === undefined) return null;
	const { region, stopped, serving } = found;
	const changes = specChanges(serving, stopped);
	const servingLabel = `${depTag(serving)} (${formatShortId(serving.id)})`;
	return (
		<Banner
			tone="warn"
			className="mx-0 mt-0"
			title={`These settings are from deployment ${formatShortId(stopped.id)}, which did not finish in ${region.name}.`}
		>
			<p className="my-1">
				<span className="font-mono">{servingLabel}</span> still serves.
			</p>
			{changes.length > 0 && (
				<dl className="my-2 flex flex-col gap-0.5 font-mono text-[12px]">
					<dt className="font-sans text-fg2">Serving → these settings</dt>
					{changes.map((c) => (
						<dd key={c.label} className="m-0 [overflow-wrap:anywhere]">
							{c.label}: <span className="text-bad-fg">{c.from ?? ABSENT}</span> →{" "}
							<span className="text-ok-fg">{c.to ?? ABSENT}</span>
						</dd>
					))}
				</dl>
			)}
			{canRollBackTo(serving, region) && (
				<Button
					size="sm"
					disabled={!canWrite}
					title={canWrite ? undefined : NEEDS_WRITE}
					onClick={() => {
						onRollback(serving, stopped);
					}}
				>
					<Undo2Icon />
					Roll back to {depTag(serving)}
				</Button>
			)}
		</Banner>
	);
}

export function SecretsChangedNotice({
	id,
	names,
	canWrite,
	onRestart,
}: {
	id: string;
	names: readonly string[];
	canWrite: boolean;
	onRestart: () => void;
}) {
	if (names.length === 0) return null;
	return (
		<InfoNotice
			id={id}
			action={
				canWrite ? (
					<Button variant="outline" size="sm" onClick={onRestart}>
						<RotateCwIcon />
						Restart
					</Button>
				) : undefined
			}
		>
			{names.map((name, i) => (
				<Fragment key={name}>
					{i > 0 && ", "}
					<code>{name}</code>
				</Fragment>
			))}{" "}
			changed after this deployment. Restart to use the new {names.length > 1 ? "values" : "value"}.
		</InfoNotice>
	);
}
