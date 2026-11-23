import { Fragment } from "react";
import { RotateCwIcon, Undo2Icon, XIcon } from "lucide-react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { Notice as NoticeBox } from "@/components/design/Notice";
import { NEEDS_WRITE } from "@/hooks/useMyScopes";
import { formatShortId } from "@/lib/utils";

import { servingBehindStop } from "./deployment-progress";
import { canRollBackTo, depTag, type Notice, type RegionView } from "./model";
import { ABSENT, specChanges } from "./spec";

export function NoticeBanner({ notice, onDismiss }: { notice: Notice; onDismiss: () => void }) {
	return (
		<NoticeBox
			tone={notice.tone}
			role="status"
			title={notice.title}
			action={
				<Button variant="ghost" size="icon-sm" aria-label="Dismiss" onClick={onDismiss}>
					<XIcon />
				</Button>
			}
		>
			<span className="break-words whitespace-pre-line">{notice.message}</span>
		</NoticeBox>
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
		<NoticeBox
			id="serving-notice"
			tone="warn"
			title={`These settings are from deployment ${formatShortId(stopped.id)}, which did not finish in ${region.name}.`}
			detail={
				changes.length > 0 ? (
					<dl className="m-0 flex flex-col">
						<dt className="font-sans text-[12.5px]">Serving → these settings</dt>
						{changes.map((c) => (
							<dd key={c.label} className="m-0">
								{c.label}: <span className="text-err">{c.from ?? ABSENT}</span> →{" "}
								<span className="text-ok">{c.to ?? ABSENT}</span>
							</dd>
						))}
					</dl>
				) : undefined
			}
			action={
				canRollBackTo(serving, region) ? (
					<Button
						variant="outline"
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
				) : undefined
			}
		>
			<span className="font-mono">{servingLabel}</span> still serves.
		</NoticeBox>
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
		<NoticeBox
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
		</NoticeBox>
	);
}
