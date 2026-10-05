import { useQuery } from "@connectrpc/connect-query";
import type { ReactNode } from "react";

import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/design/Avatar";
import { Button } from "@/components/design/Button";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useBreadcrumbs } from "@/context/ShellContext";

import { DeleteAccountSection } from "./profile/DeleteAccountSection";
import { TokensPreview } from "./profile/TokensPreview";

function Row({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="grid gap-2 border-b border-line px-5 py-[14px] last:border-b-0 md:grid-cols-[240px_minmax(0,1fr)] md:gap-6">
			<span className="font-semibold">{label}</span>
			<div className="min-w-0 text-fg2">{children}</div>
		</div>
	);
}

function ProfileSkeleton() {
	return (
		<Page className="max-w-[1080px]">
			<Skeleton className="h-7 w-32" />
			<div className="rounded-lg border border-line">
				{[0, 1, 2].map((i) => (
					<div key={i} className="flex items-center gap-6 border-b border-line px-5 py-4 last:border-b-0">
						<Skeleton className="h-3.5 w-24" />
						<Skeleton className="h-3.5 w-56" />
					</div>
				))}
			</div>
		</Page>
	);
}

export function Profile() {
	useBreadcrumbs("Profile");
	const { logout } = useAuth();
	const { data: whoAmIResponse, isLoading, error } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;

	if (isLoading) {
		return <ProfileSkeleton />;
	}

	if (!user) {
		return <ErrorCard error={error} fallbackMessage="User not found" />;
	}

	const initial = user.name.charAt(0).toUpperCase();

	return (
		<Page className="max-w-[1080px]">
			<PageHeader
				title="Profile"
				actions={
					<Button
						variant="outline"
						onClick={() => {
							void logout();
						}}
					>
						Log out
					</Button>
				}
			/>
			<Section title="Account">
				<Row label="Avatar">
					<Avatar className="size-10">
						<AvatarImage src={user.avatarUrl} alt="user avatar" />
						<AvatarFallback>{initial}</AvatarFallback>
					</Avatar>
				</Row>
				<Row label="Name">
					<span className="text-foreground">{user.name}</span>
				</Row>
				<Row label="Email">{user.email}</Row>
			</Section>
			<TokensPreview userId={user.id} />
			<DeleteAccountSection userId={user.id} />
		</Page>
	);
}
