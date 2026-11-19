import { DeploymentActorType, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { KeyRoundIcon } from "lucide-react";

import { deploymentActor, deploymentMethod, shortSha } from "@/lib/deployment-utils";
import { formatShortId } from "@/lib/utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

export function DeploymentActor({ deployment: d }: { deployment: Deployment }) {
	const actor = deploymentActor(d);
	const method = deploymentMethod(d);
	return (
		<span className="flex min-w-0 items-center gap-2.5">
			{d.actorType === DeploymentActorType.API_TOKEN ? (
				<span
					aria-hidden
					className="flex size-[22px] shrink-0 items-center justify-center rounded-md border border-line2 text-fg2"
				>
					<KeyRoundIcon className="size-3" />
				</span>
			) : (
				<MemberAvatar
					name={d.actorName}
					email=""
					avatarUrl={d.actorAvatarUrl}
					className="size-[22px] shrink-0 text-[9.5px]"
				/>
			)}
			<span className="flex min-w-0 flex-col gap-px">
				<span className="truncate text-[13px] text-foreground" title={actor}>
					{actor}
				</span>
				{method !== "" && <span className="truncate font-mono text-[11px] text-fg3">{method}</span>}
			</span>
		</span>
	);
}

export function DeploymentId({ deployment: d }: { deployment: Deployment }) {
	return (
		<span className="flex min-w-0 flex-col gap-px">
			<span className="truncate font-mono text-[12.5px] text-foreground" title={d.id}>
				{formatShortId(d.id)}
			</span>
			{d.commit !== undefined && (
				<span className="truncate font-mono text-[11px] text-fg3" title={d.commit.sha}>
					{shortSha(d.commit.sha)}
				</span>
			)}
		</span>
	);
}
