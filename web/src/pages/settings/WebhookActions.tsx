import { useMutation } from "@connectrpc/connect-query";
import type { Webhook } from "@gen/loco/webhook/v1/webhook_pb";
import { rotateWebhookSecret, sendTestEvent } from "@gen/loco/webhook/v1/webhook-WebhookService_connectquery";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/design/Button";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { formatClock, tsMs } from "@/lib/time";

import { NewWebhookDialog, SigningSecret } from "./NewWebhookDialog";
import { formatDay } from "./parts";

export function WebhookActions({
	workspaceId,
	workspaceName,
	webhook,
	onChanged,
}: {
	workspaceId: string;
	workspaceName: string;
	webhook: Webhook;
	onChanged: () => void;
}) {
	const [editing, setEditing] = useState(false);
	const [rotating, setRotating] = useState(false);
	const test = useMutation(sendTestEvent);
	return (
		<>
			<div className="flex flex-wrap gap-2">
				<Button
					variant="outline"
					onClick={() => {
						setEditing(true);
					}}
				>
					Edit webhook
				</Button>
				<Button
					variant="outline"
					onClick={() => {
						setRotating(true);
					}}
				>
					Rotate secret
				</Button>
				<Button
					variant="outline"
					disabled={test.isPending}
					onClick={() => {
						test.mutate(
							{ workspaceId, webhookId: webhook.id },
							{
								onSuccess: () => {
									toast.success("Test event queued");
									onChanged();
								},
								onError: (error) => {
									toastConnectError(error, "Could not send the test event");
								},
							},
						);
					}}
				>
					Send test event
				</Button>
			</div>
			{webhook.previousSecretExpiresAt !== undefined && (
				<p className="m-0 text-[12.5px] text-fg3">
					Previous secret expires {formatDay(webhook.previousSecretExpiresAt)} at{" "}
					{formatClock(tsMs(webhook.previousSecretExpiresAt))}.
				</p>
			)}
			<NewWebhookDialog
				workspaceId={workspaceId}
				workspaceName={workspaceName}
				webhook={webhook}
				open={editing}
				onClose={() => {
					setEditing(false);
				}}
				onCreated={onChanged}
			/>
			<RotateSecret
				open={rotating}
				workspaceId={workspaceId}
				webhook={webhook}
				onChanged={onChanged}
				onClose={() => {
					setRotating(false);
				}}
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
	const rotate = useMutation(rotateWebhookSecret);
	const [hours, setHours] = useState("24");
	const [secret, setSecret] = useState<string | null>(null);
	const seconds = Number(hours) * 3600;
	const valid = hours.trim() !== "" && Number.isInteger(seconds) && seconds >= 0 && seconds <= 604800;
	const close = () => {
		setSecret(null);
		onClose();
	};
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next && !rotate.isPending) close();
			}}
		>
			<DialogContent title={secret === null ? "Rotate signing secret" : "Signing secret"}>
				{secret !== null ? (
					<SigningSecret secret={secret} onDone={close} />
				) : (
					<form
						noValidate
						className="contents"
						onSubmit={(e) => {
							e.preventDefault();
							if (!valid || rotate.isPending) return;
							rotate.mutate(
								{ workspaceId, webhookId: webhook.id, gracePeriodSeconds: seconds },
								{
									onSuccess: (res) => {
										setSecret(res.secret);
										onChanged();
									},
								},
							);
						}}
					>
						<DialogBody>
							<p className="m-0 text-fg2">
								Deliveries use the new secret immediately. During the grace period they also carry a signature from the
								replaced secret. Rotating again drops the older secret.
							</p>
							<Field
								label="Grace period (hours)"
								hint="Use 0 to stop signing with the replaced secret immediately, or up to 168 hours."
								error={valid ? undefined : "Enter a grace period from 0 to 168 hours."}
							>
								<Input
									inputMode="decimal"
									value={hours}
									aria-invalid={!valid}
									onChange={(e) => {
										setHours(e.target.value);
									}}
								/>
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
							<Button type="submit" disabled={!valid || rotate.isPending}>
								Rotate secret
							</Button>
						</DialogFooter>
					</form>
				)}
			</DialogContent>
		</Dialog>
	);
}
