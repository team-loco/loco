import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import {
	grantSystemRole,
	listSystemRoles,
	revokeSystemRole,
} from "@gen/loco/admin/v1/admin-SystemRoleService_connectquery";
import { SystemRole, type SystemRoleHolder } from "@gen/loco/admin/v1/admin_pb";
import { getUser } from "@gen/loco/user/v1/user-UserService_connectquery";
import { useQueryClient } from "@tanstack/react-query";
import { ShieldCheckIcon, ShieldIcon, UserMinusIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { Dialog, DialogBody, DialogContent, DialogFooter } from "@/components/design/Dialog";
import { DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel } from "@/components/design/DropdownMenu";
import { Field } from "@/components/design/Field";
import { InlineCode } from "@/components/design/InlineCode";
import { Input } from "@/components/design/Input";
import { RowMenu } from "@/components/design/RowMenu";
import { SegmentedChoice } from "@/components/design/SegmentedChoice";
import { toastConnectError } from "@/lib/error-handler";
import { effectiveRole, roleChangeBlock, SYSTEM_ROLE_LABEL, type RoleChange } from "@/lib/system";
import { toast } from "@/lib/toast";
import { userLabel } from "@/lib/user";
import { isPlainEmail } from "@/pages/team/model";

const ROLE_CHOICES = [
	{ value: "read", label: "Read" },
	{ value: "admin", label: "Admin" },
] as const;

const ROLE_HINT: Record<"read" | "admin", string> = {
	read: "Can read everything in the installation and change nothing.",
	admin: "Can read everything and administer the installation, including who holds a role.",
};

function useRefreshRoles() {
	const queryClient = useQueryClient();
	return async () => {
		await queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listSystemRoles, cardinality: undefined }),
		});
	};
}

/** Grants an existing user a role, found by email. */
export function GrantRoleDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
	const [email, setEmail] = useState("");
	const [role, setRole] = useState<"read" | "admin">("read");
	const grant = useMutation(grantSystemRole);
	const refresh = useRefreshRoles();
	const address = email.trim();
	const valid = isPlainEmail(address);
	const found = useQuery(getUser, { key: { case: "email", value: address } }, { enabled: valid, retry: false });
	const user = valid ? found.data?.user : undefined;

	const close = () => {
		setEmail("");
		setRole("read");
		onClose();
	};
	const submit = async () => {
		if (user === undefined) return;
		try {
			await grant.mutateAsync({ userId: user.id, role: role === "admin" ? SystemRole.ADMIN : SystemRole.READ });
			toast(`${userLabel(user)} now holds the ${role} role`);
			close();
			await refresh();
		} catch (err) {
			toastConnectError(err, `Failed to grant the ${role} role`);
		}
	};

	let hint = "The person needs an account. Type the email they sign in with.";
	if (valid && found.isFetching) hint = "Looking for the account…";
	else if (valid && user !== undefined) hint = `Account found: ${userLabel(user)}.`;
	const missing = valid && !found.isFetching && user === undefined;

	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!next) close();
			}}
		>
			<DialogContent size="form" title="Grant an installation role">
				<form
					className="contents"
					onSubmit={(e) => {
						e.preventDefault();
						void submit();
					}}
				>
					<DialogBody>
						<Field
							label="Email"
							hint={hint}
							error={missing ? "No user has this email. They need to sign in once first." : undefined}
						>
							<Input
								autoFocus
								type="email"
								value={email}
								placeholder="ana@example.com"
								onChange={(e) => {
									setEmail(e.target.value);
								}}
							/>
						</Field>
						<Field label="Role" hint={ROLE_HINT[role]}>
							<SegmentedChoice
								label="Role"
								options={ROLE_CHOICES}
								value={role}
								className="sm:flex"
								onChange={(next) => {
									setRole(next === "admin" ? "admin" : "read");
								}}
							/>
						</Field>
					</DialogBody>
					<DialogFooter>
						<Button type="button" variant="outline" onClick={close}>
							Cancel
						</Button>
						<Button type="submit" disabled={user === undefined || grant.isPending}>
							{grant.isPending ? "Granting…" : `Grant ${role} role`}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}

/** The row menu of a role holder: change the role or revoke the grant, each behind a confirm step. */
export function OperatorRowMenu({
	holder,
	holders,
}: {
	holder: SystemRoleHolder;
	holders: readonly SystemRoleHolder[];
}) {
	const [pending, setPending] = useState<RoleChange | null>(null);
	const grant = useMutation(grantSystemRole);
	const revoke = useMutation(revokeSystemRole);
	const refresh = useRefreshRoles();
	const isAdmin = effectiveRole(holder) === SystemRole.ADMIN;
	const change: RoleChange = isAdmin ? "read" : "admin";
	const changeBlock = roleChangeBlock(holder, change, holders);
	const revokeBlock = roleChangeBlock(holder, "revoke", holders);

	const confirm = async () => {
		if (pending === null) return;
		try {
			if (pending === "revoke") {
				await revoke.mutateAsync({ userId: holder.userId });
				toast(`Revoked the grant of ${holder.email}`);
			} else {
				await grant.mutateAsync({
					userId: holder.userId,
					role: pending === "admin" ? SystemRole.ADMIN : SystemRole.READ,
				});
				toast(`${holder.email} now holds the ${pending} role`);
			}
			setPending(null);
			await refresh();
		} catch (err) {
			toastConnectError(err, "Failed to change the role");
		}
	};

	const name = <InlineCode>{holder.email}</InlineCode>;
	const copy: Record<RoleChange, { title: string; confirm: string; body: React.ReactNode }> = {
		admin: {
			title: "Make admin",
			confirm: "Make admin",
			body: <>{name} gains the installation administration: clusters, users and who holds a role.</>,
		},
		read: {
			title: "Make reader",
			confirm: "Make reader",
			body: <>{name} keeps read access to the installation and loses the administration.</>,
		},
		revoke: {
			title: "Revoke grant",
			confirm: "Revoke grant",
			body: (
				<>
					{name} loses the role this grant gave.
					{holder.configuredRole !== SystemRole.UNSPECIFIED &&
						` The configured ${SYSTEM_ROLE_LABEL[holder.configuredRole].toLowerCase()} role stays.`}
				</>
			),
		},
	};

	return (
		<>
			<RowMenu label={`Actions for ${holder.email}`} title={holder.email} monoTitle>
				<DropdownMenuItem
					disabled={changeBlock !== undefined}
					onClick={() => {
						setPending(change);
					}}
				>
					{isAdmin ? <ShieldIcon /> : <ShieldCheckIcon />}
					{copy[change].title}
				</DropdownMenuItem>
				{changeBlock !== undefined && <BlockReason>{changeBlock}</BlockReason>}
				<DropdownMenuItem
					variant="destructive"
					disabled={revokeBlock !== undefined}
					onClick={() => {
						setPending("revoke");
					}}
				>
					<UserMinusIcon />
					Revoke grant
				</DropdownMenuItem>
				{revokeBlock !== undefined && <BlockReason>{revokeBlock}</BlockReason>}
			</RowMenu>
			<ConfirmDialog
				open={pending !== null}
				title={pending === null ? "" : copy[pending].title}
				description={pending === null ? "" : copy[pending].body}
				confirmLabel={pending === null ? "" : copy[pending].confirm}
				confirmVariant={pending === "revoke" ? "destructive" : "default"}
				pending={grant.isPending || revoke.isPending}
				onConfirm={() => {
					void confirm();
				}}
				onClose={() => {
					setPending(null);
				}}
			/>
		</>
	);
}

function BlockReason({ children }: { children: string }) {
	return (
		<DropdownMenuGroup>
			<DropdownMenuLabel className="max-w-[260px] text-[12px] leading-snug font-normal text-fg3">
				{children}
			</DropdownMenuLabel>
		</DropdownMenuGroup>
	);
}
