import { useMutation, useQuery } from "@connectrpc/connect-query";
import { getUser, setUserStatus } from "@gen/loco/user/v1/user-UserService_connectquery";
import { UserStatus, type User } from "@gen/loco/user/v1/user_pb";
import { BanIcon, RotateCcwIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { CopyId } from "@/components/design/CopyId";
import { InlineCode } from "@/components/design/InlineCode";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
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
		body: (email: string) => (
			<>
				<InlineCode>{email}</InlineCode> can sign in again.
			</>
		),
	},
} as const;

function UserBody({ user, canChange }: { user: User; canChange: boolean }) {
	const [pending, setPending] = useState<UserStatus.SUSPENDED | UserStatus.ACTIVE | null>(null);
	const mutation = useMutation(setUserStatus);

	const confirm = async () => {
		if (pending === null) return;
		try {
			await mutation.mutateAsync({ key: { case: "userId", value: user.id }, status: pending });
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
					<Fact term="Email">
						<span className="font-mono text-[12.5px]">{user.email}</span>
					</Fact>
					<Fact term="Joined">
						<SystemTime ts={user.createdAt} />
					</Fact>
					<Fact term="User ID">
						<CopyId value={user.id} title="Copy user ID" className="-ml-1.5 font-mono text-[12.5px] text-foreground">
							{user.id}
						</CopyId>
					</Fact>
				</dl>
				{canChange && (
					<div className="flex flex-col gap-2">
						<span className="text-[12.5px] text-fg3">
							Suspending blocks sign-in and API access. Reinstating lets the user back in.
						</span>
						<div className="flex gap-2">
							<Button
								variant="destructive-outline"
								size="sm"
								onClick={() => {
									setPending(UserStatus.SUSPENDED);
								}}
							>
								<BanIcon />
								Suspend
							</Button>
							<Button
								variant="outline"
								size="sm"
								onClick={() => {
									setPending(UserStatus.ACTIVE);
								}}
							>
								<RotateCcwIcon />
								Reinstate
							</Button>
						</div>
					</div>
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
				{found.error !== null && (
					<ErrorCard error={found.error} fallbackMessage="Failed to load the user" minHeight="min-h-48" />
				)}
				{user !== undefined && <UserBody key={user.id} user={user} canChange={canChange} />}
			</SheetContent>
		</Sheet>
	);
}
