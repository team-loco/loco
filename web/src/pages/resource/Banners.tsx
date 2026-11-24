import { Fragment } from "react";
import { RotateCwIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { InlineCode } from "@/components/design/InlineCode";
import { Notice as NoticeBox } from "@/components/design/Notice";

import { servingBehindStop } from "./deployment-progress";
import { type Notice, type RegionView } from "./model";

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

export function ServingBanner({ regions }: { regions: RegionView[] }) {
	if (servingBehindStop(regions) === undefined) return null;
	return (
		<NoticeBox id="serving-notice" tone="warn">
			These settings are from a newer deployment which hasn’t finished rolling out.
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
					<InlineCode>{name}</InlineCode>
				</Fragment>
			))}{" "}
			changed after this deployment. Restart to use the new {names.length > 1 ? "values" : "value"}.
		</NoticeBox>
	);
}
