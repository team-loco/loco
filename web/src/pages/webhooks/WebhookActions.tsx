import { useMutation } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import { rotateWebhookSecret, sendTestEvent } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useCopy } from "@/hooks/useCopy";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { CopyIcon, PencilIcon, RotateCwIcon, Trash2Icon } from "lucide-react";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";

import { NewWebhookDialog, SigningSecret } from "./NewWebhookDialog";

export function WebhookActions({
	workspaceId,
	workspaceName,
	webhook,
	onChanged,
	onDelete,
	menuOnly = false,
}: {
	onDelete: () => void;
	menuOnly?: boolean;
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook;
	onChanged: () => void;
}) {
	const [, copy] = useCopy();
	const [dialog, setDialog] = useState<"edit" | "rotate" | null>(null);
	const close = () => {
		setDialog(null);
	};
	const test = useMutation(sendTestEvent, {
		onSuccess: () => {
			toast.success("Sent a test event");
			onChanged();
		},
		onError: (error) => {
			toastConnectError(error, "Could not send the test event");
		},
	});
	return (
		<>
			<div className="flex shrink-0 items-center gap-1">
				{!menuOnly && (
					<Button
						variant="ghost"
						aria-disabled={test.isPending}
						onClick={() => {
							if (!test.isPending) test.mutate({ workspaceId, webhookId: webhook.id });
						}}
					>
						Send test event
					</Button>
				)}
				<RowMenu label={`Actions for ${webhook.url}`}>
					{menuOnly && (
						<DropdownMenuItem
							aria-disabled={test.isPending}
							onClick={() => {
								if (!test.isPending) test.mutate({ workspaceId, webhookId: webhook.id });
							}}
						>
							Send test event
						</DropdownMenuItem>
					)}
					<DropdownMenuItem
						onClick={() => {
							setDialog("edit");
						}}
					>
						<PencilIcon />
						Edit
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={() => {
							setDialog("rotate");
						}}
					>
						<RotateCwIcon />
						Rotate signing secret
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={() => {
							copy("url", webhook.url, "Copied URL");
						}}
					>
						<CopyIcon />
						Copy URL
					</DropdownMenuItem>
					{!menuOnly && (
						<DropdownMenuItem
							onClick={() => {
								copy("id", webhook.id, "Copied webhook ID");
							}}
						>
							<CopyIcon />
							Copy webhook ID
						</DropdownMenuItem>
					)}
					<DropdownMenuSeparator />
					<DropdownMenuItem variant="destructive" onClick={onDelete}>
						<Trash2Icon />
						Delete
					</DropdownMenuItem>
				</RowMenu>
			</div>
			<NewWebhookDialog
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				webhook={webhook}
				open={dialog === "edit"}
				onClose={close}
				onCreated={onChanged}
			/>
			<RotateSecret
				open={dialog === "rotate"}
				workspaceId={workspaceId}
				webhook={webhook}
				onChanged={onChanged}
				onClose={close}
			/>
		</>
	);
}

function RotateSecret({
	open,
	workspaceId,
	webhook,
	onChanged,
	onClose,
}: {
	open: boolean;
	workspaceId: string;
	webhook: Webhook;
	onChanged: () => void;
	onClose: () => void;
}) {
	const rotate = useMutation(rotateWebhookSecret, { gcTime: 0, onSuccess: onChanged });
	const [seconds, setSeconds] = useState("86400");
	const secret = rotate.data?.secret;
	const close = () => {
		rotate.reset();
		onClose();
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && !rotate.isPending) close();
			}}
		>
			<DialogContent title={secret === undefined ? "Rotate signing secret" : "New signing secret"}>
				{secret !== undefined ? (
					<SigningSecret secret={secret} onDone={close} />
				) : (
					<form
						noValidate
						className="contents"
						onSubmit={(e) => {
							e.preventDefault();
							if (rotate.isPending) return;
							rotate.mutate({
								workspaceId,
								webhookId: webhook.id,
								gracePeriodSeconds: Number(seconds),
							});
						}}
					>
						<DialogBody>
							<p className="m-0 break-all text-fg2">For {webhook.url}</p>
							<Field
								label="Keep the old secret working for"
								hint={
									seconds === "0"
										? "The old secret stops working right away."
										: `Deliveries carry both signatures for ${GRACES.find((g) => g.value === seconds)?.label}, so you can update your receiver before the old secret stops working.`
								}
							>
								<ToggleGroup
									variant="segmented"
									aria-label="Grace period"
									value={[seconds]}
									onValueChange={(v) => {
										if (v[0] !== undefined) setSeconds(v[0]);
									}}
								>
									{GRACES.map((g) => (
										<ToggleGroupItem key={g.value} value={g.value}>
											{g.label}
										</ToggleGroupItem>
									))}
								</ToggleGroup>
							</Field>
							{rotate.error !== null && (
								<p role="alert" className="m-0 text-bad-fg">
									{getErrorMessage(rotate.error, "Could not rotate the secret")}
								</p>
							)}
						</DialogBody>
						<DialogFooter>
							<Button variant="outline" type="button" disabled={rotate.isPending} onClick={close}>
								Cancel
							</Button>
							<Button type="submit" aria-disabled={rotate.isPending}>
								Rotate
							</Button>
						</DialogFooter>
					</form>
				)}
			</DialogContent>
		</Dialog>
	);
}

const GRACES = [
	{ value: "0", label: "None" },
	{ value: "3600", label: "1 hour" },
	{ value: "86400", label: "24 hours" },
	{ value: "604800", label: "7 days" },
];
