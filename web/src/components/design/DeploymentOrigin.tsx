import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentOrigin } from "@/lib/deployment-utils";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

export function DeploymentOrigin({ deployment }: { deployment: Deployment }) {
	const label = deploymentOrigin(deployment);
	return (
		<span className="flex min-w-0 items-center gap-2 text-[12.5px] text-fg2" title={label}>
			{deployment.actorAvatarUrl !== "" && (
				<MemberAvatar
					name={deployment.actorName}
					email=""
					avatarUrl={deployment.actorAvatarUrl}
					className="size-5 shrink-0"
				/>
			)}
			<span className="truncate">{label}</span>
		</span>
	);
}
