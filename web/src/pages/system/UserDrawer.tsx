import { useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useAuth } from "@/auth/AuthProvider";
import { orgPath } from "@/lib/system";
import { createConnectQueryKey, useMutation, useQuery } from "@connectrpc/connect-query";
import { getUser, listUsers, setUserStatus } from "@gen/loco/user/v1/user-UserService_connectquery";
import { UserStatus, type User } from "@gen/loco/user/v1/user_pb";
import { BanIcon, RotateCcwIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { ErrorCard } from "@/components/ErrorCard";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { CopyId } from "@/components/design/CopyId";
import { InlineCode } from "@/components/design/InlineCode";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { Skeleton } from "@/components/design/Skeleton";
import { toastConnectError } from "@/lib/error-handler";
import { toast } from "@/lib/toast";
import { userLabel } from "@/lib/user";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { SystemTime } from "./shared";

function Fact({ term, children }: { term: string; children: ReactNode }) {
	return (
		<div className="grid grid-cols-[110px_minmax(0,1fr)] items-baseline gap-x-4 py-1.5">
			<dt className="text-fg3">{term}</dt>
			<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{children}</dd>
		</div>
	);
}

const STATUS_COPY = {
	[UserStatus.SUSPENDED]: {
		label: "Suspend",
		confirm: "Suspend user",
		done: "Suspended",
		icon: <BanIcon />,
		variant: "destructive-outline",
		body: (email: string) => (
			<>
				<InlineCode>{email}</InlineCode> can no longer sign in, and their sessions and tokens stop working. Reinstate
				lets them back in.
			</>
		),
	},
	[UserStatus.ACTIVE]: {
		label: "Reinstate",
		confirm: "Reinstate user",
		done: "Reinstated",
		icon: <RotateCcwIcon />,
		variant: "outline",
		body: (email: string) => (
			<>
				<InlineCode>{email}</InlineCode> can sign in again.
			</>
		),
	},
} as const;

function Organizations({ userId }: { userId: string }) {
	const orgs = useQuery(listUserOrgs, { userId, pageSize: 200 });
	return (
		<div className="flex flex-col gap-2">
			<h3 className="m-0 text-[13px] font-medium">Organizations</h3>
			{orgs.error !== null ? (
				<ErrorCard error={orgs.error} fallbackMessage="Failed to load the organizations" minHeight="min-h-24" />
			) : orgs.data === undefined ? (
				<Skeleton className="h-16 w-full" />
			) : orgs.data.orgs.length === 0 ? (
				<span className="text-[13px] text-fg3">Not a member of any organization.</span>
			) : (
				<ul aria-label="Organizations" className="m-0 flex list-none flex-col gap-1.5 p-0 text-[13px]">
					{orgs.data.orgs.map((org) => (
						<li key={org.id} className="flex items-center gap-2">
							<Link
								to={orgPath(org.id)}
								className="min-w-0 truncate text-fg2 underline decoration-line2 underline-offset-4 hover:decoration-current"
							>
								{org.name}
							</Link>
							{org.suspendedAt !== undefined && (
								<Badge tone="bad" size="sm">
									Suspended
								</Badge>
							)}
						</li>
					))}
				</ul>
			)}
		</div>
	);
}

function UserBody({ user, canChange }: { user: User; canChange: boolean }) {
	const [pending, setPending] = useState<UserStatus.SUSPENDED | UserStatus.ACTIVE | null>(null);
	const mutation = useMutation(setUserStatus);
	const queryClient = useQueryClient();
	const self = useAuth().user?.id === user.id;
	const suspended = user.status === UserStatus.SUSPENDED;
	const status = suspended ? UserStatus.ACTIVE : UserStatus.SUSPENDED;

	const confirm = async () => {
		if (pending === null) return;
		try {
			await mutation.mutateAsync({ key: { case: "userId", value: user.id }, status: pending });
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getUser, cardinality: undefined }) }),
				queryClient.invalidateQueries({
					queryKey: createConnectQueryKey({ schema: listUsers, cardinality: undefined }),
				}),
			]);
			toast(`${STATUS_COPY[pending].done} ${userLabel(user)}`);
			setPending(null);
		} catch (err) {
			toastConnectError(err, `Failed to ${STATUS_COPY[pending].label.toLowerCase()} ${userLabel(user)}`);
		}
	};

	return (
		<>
			<SheetBody>
				<dl className="m-0 text-[13px]">
					<Fact term="Status">
						<Badge tone={suspended ? "bad" : "ok"} size="sm">
							{suspended ? "Suspended" : "Active"}
						</Badge>
					</Fact>
					<Fact term="Email">
						<span className="font-mono text-[12.5px]">{user.email}</span>
					</Fact>
					<Fact term="Joined">
						<SystemTime ts={user.createdAt} />
					</Fact>
					<Fact term="User ID">
						<CopyId
							value={user.id}
							title="Copy user ID"
							className="-ml-1.5 max-w-full text-left font-mono text-[12.5px] whitespace-normal text-foreground"
						>
							<span className="min-w-0 break-all">{user.id}</span>
						</CopyId>
					</Fact>
				</dl>
				<Organizations userId={user.id} />
				{canChange && !self && (
					<Button
						variant={STATUS_COPY[status].variant}
						size="sm"
						className="self-start"
						onClick={() => {
							setPending(status);
						}}
					>
						{STATUS_COPY[status].icon}
						{STATUS_COPY[status].label}
					</Button>
				)}
			</SheetBody>
			<ConfirmDialog
				open={pending !== null}
				title={pending === null ? "" : `${STATUS_COPY[pending].label}: ${userLabel(user)}`}
				description={pending === null ? "" : STATUS_COPY[pending].body(user.email)}
				confirmLabel={pending === null ? "" : STATUS_COPY[pending].confirm}
				confirmVariant={pending === UserStatus.SUSPENDED ? "destructive" : "default"}
				pending={mutation.isPending}
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

/** The drawer of one user; `userId` null keeps it closed. */
export function UserDrawer({
	userId,
	canChange,
	onClose,
}: {
	userId: string | null;
	canChange: boolean;
	onClose: () => void;
}) {
	const found = useQuery(getUser, { key: { case: "userId", value: userId ?? "" } }, { enabled: userId !== null });
	const user = found.data?.user;
	return (
		<Sheet
			open={userId !== null}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent size="wide">
				<SheetHeader>
					{user !== undefined && (
						<>
							<MemberAvatar
								name={user.name}
								email={user.email}
								avatarUrl={user.avatarUrl}
								className="size-[26px] shrink-0"
							/>
							<span className="flex min-w-0 items-baseline gap-2">
								<SheetTitle className="truncate">{userLabel(user)}</SheetTitle>
								<SheetDescription className="sr-only">Account details</SheetDescription>
							</span>
						</>
					)}
					{user === undefined && <SheetTitle>User</SheetTitle>}
				</SheetHeader>
				{found.isLoading && (
					<SheetBody>
						<Skeleton className="h-24 w-full" />
						<Skeleton className="h-16 w-full" />
					</SheetBody>
				)}
				{found.error !== null && (
					<ErrorCard error={found.error} fallbackMessage="Failed to load the user" minHeight="min-h-48" />
				)}
				{user !== undefined && <UserBody key={user.id} user={user} canChange={canChange} />}
			</SheetContent>
		</Sheet>
	);
}
