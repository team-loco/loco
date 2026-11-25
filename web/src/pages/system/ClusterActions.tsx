import { useMutation } from "@connectrpc/connect-query";
import {
	issueJoinToken,
	pauseCluster,
	removeCluster,
	restoreCluster,
	resumeCluster,
} from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import type { Cluster, JoinToken as JoinTokenMessage } from "@gen/loco/cluster/v1/cluster_pb";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRoundIcon, PauseIcon, PlayIcon, RotateCcwIcon, Trash2Icon } from "lucide-react";
import { Fragment, useState, type ReactNode } from "react";

import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Button } from "@/components/design/Button";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { InlineCode } from "@/components/design/InlineCode";
import { RowMenu } from "@/components/design/RowMenu";
import { toastConnectError } from "@/lib/error-handler";
import type { ClusterState } from "@/lib/system";
import { toast } from "@/lib/toast";

import { JoinToken } from "./JoinToken";
import { refreshFleet } from "./shared";

export type ClusterAction = "token" | "pause" | "resume" | "remove" | "restore";

interface ActionCopy {
	label: string;
	icon: ReactNode;
	confirm: string;
	done: string;
	destructive: boolean;
	describe: (name: string) => ReactNode;
}

const ACTIONS: Record<ClusterAction, ActionCopy> = {
	token: {
		label: "Issue join token",
		icon: <KeyRoundIcon />,
		confirm: "Issue token",
		done: "",
		destructive: false,
		describe: (name) => (
			<>
				Issues a new join token for <InlineCode>{name}</InlineCode>. Any earlier token stops working. The token is shown
				once.
			</>
		),
	},
	pause: {
		label: "Pause",
		icon: <PauseIcon />,
		confirm: "Pause cluster",
		done: "Paused",
		destructive: false,
		describe: (name) => (
			<>
				<InlineCode>{name}</InlineCode> takes no new placements while it is paused. Workloads already on it keep
				running.
			</>
		),
	},
	resume: {
		label: "Resume",
		icon: <PlayIcon />,
		confirm: "Resume cluster",
		done: "Resumed",
		destructive: false,
		describe: (name) => (
			<>
				<InlineCode>{name}</InlineCode> takes new placements again.
			</>
		),
	},
	remove: {
		label: "Remove",
		icon: <Trash2Icon />,
		confirm: "Remove cluster",
		done: "Removed",
		destructive: true,
		describe: (name) => (
			<>
				Removes <InlineCode>{name}</InlineCode> from Loco. It is refused while workloads run on it. Restore brings it
				back; the Kubernetes cluster itself is not touched.
			</>
		),
	},
	restore: {
		label: "Restore",
		icon: <RotateCcwIcon />,
		confirm: "Restore cluster",
		done: "Restored",
		destructive: false,
		describe: (name) => (
			<>
				Restores <InlineCode>{name}</InlineCode>. It keeps its agent key and stays paused until it is resumed.
			</>
		),
	},
};

/** The actions a cluster in this state accepts, in menu order. */
export function actionsFor(state: ClusterState): ClusterAction[] {
	if (state === "removed") return ["restore"];
	if (state === "paused") return ["resume", "token", "remove"];
	return ["pause", "token"];
}

/** Runs a cluster's actions behind a confirm step; render `dialogs` once beside the control that calls `request`. */
export function useClusterActions(cluster: Cluster) {
	const queryClient = useQueryClient();
	const [pending, setPending] = useState<ClusterAction | null>(null);
	const [issued, setIssued] = useState<JoinTokenMessage | null>(null);
	const mutations = {
		token: useMutation(issueJoinToken),
		pause: useMutation(pauseCluster),
		resume: useMutation(resumeCluster),
		remove: useMutation(removeCluster),
		restore: useMutation(restoreCluster),
	};
	const busy = pending !== null && mutations[pending].isPending;
	const request = { clusterId: cluster.id };

	const confirm = async () => {
		if (pending === null) return;
		try {
			if (pending === "token") {
				const response = await mutations.token.mutateAsync(request);
				setIssued(response.joinToken ?? null);
			} else {
				await mutations[pending].mutateAsync(request);
				toast(`${ACTIONS[pending].done} ${cluster.name}`);
			}
			setPending(null);
			await refreshFleet(queryClient);
		} catch (err) {
			toastConnectError(err, `Failed to ${ACTIONS[pending].label.toLowerCase()} ${cluster.name}`);
		}
	};

	const dialogs = (
		<>
			<ConfirmDialog
				open={pending !== null}
				title={`${pending === null ? "" : ACTIONS[pending].label}: ${cluster.name}`}
				description={pending === null ? "" : ACTIONS[pending].describe(cluster.name)}
				confirmLabel={pending === null ? "" : ACTIONS[pending].confirm}
				confirmVariant={pending !== null && ACTIONS[pending].destructive ? "destructive" : "default"}
				pending={busy}
				onConfirm={() => {
					void confirm();
				}}
				onClose={() => {
					setPending(null);
				}}
			/>
			<Dialog
				open={issued !== null}
				onOpenChange={(open) => {
					if (!open) setIssued(null);
				}}
			>
				<DialogContent size="form" title={`Join token for ${cluster.name}`}>
					<DialogBody>
						{issued !== null && <JoinToken region={cluster.region} token={issued.token} expiresAt={issued.expiresAt} />}
					</DialogBody>
					<DialogFooter>
						<Button
							onClick={() => {
								setIssued(null);
							}}
						>
							Done
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</>
	);

	return { request: setPending, dialogs };
}

/** One button per action the cluster's state accepts. */
export function ClusterActionButtons({ cluster, state }: { cluster: Cluster; state: ClusterState }) {
	const { request, dialogs } = useClusterActions(cluster);
	return (
		<>
			{actionsFor(state).map((action) => (
				<Button
					key={action}
					variant={ACTIONS[action].destructive ? "destructive-outline" : "outline"}
					size="sm"
					onClick={() => {
						request(action);
					}}
				>
					{ACTIONS[action].icon}
					{ACTIONS[action].label}
				</Button>
			))}
			{dialogs}
		</>
	);
}

/** The row menu of the clusters table. */
export function ClusterRowMenu({ cluster, state }: { cluster: Cluster; state: ClusterState }) {
	const { request, dialogs } = useClusterActions(cluster);
	const actions = actionsFor(state);
	return (
		<>
			<RowMenu label={`Actions for ${cluster.name}`} title={cluster.name} monoTitle>
				{actions.map((action) => (
					<Fragment key={action}>
						{ACTIONS[action].destructive && <DropdownMenuSeparator />}
						<DropdownMenuItem
							variant={ACTIONS[action].destructive ? "destructive" : "default"}
							onClick={() => {
								request(action);
							}}
						>
							{ACTIONS[action].icon}
							{ACTIONS[action].label}
						</DropdownMenuItem>
					</Fragment>
				))}
			</RowMenu>
			{dialogs}
		</>
	);
}
