import { useAuth } from "@/auth/AuthProvider";

export function ClaimedOrgsBanner() {
	const { claimedOrgs, dismissClaimedOrgs } = useAuth();
	if (claimedOrgs.length === 0) return null;
	return (
		<div className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-4">
			<button
				type="button"
				onClick={dismissClaimedOrgs}
				className="pointer-events-auto flex max-w-full flex-col gap-1 rounded-lg border border-info-fg bg-info-bg px-3.5 py-2.5 text-left text-base text-info-fg shadow-popover"
			>
				{claimedOrgs.map((org) => (
					<span key={org.orgId}>
						{org.inviterName} added you to <span className="font-semibold">{org.orgName}</span>
					</span>
				))}
				<span className="text-sm opacity-80">Click to dismiss</span>
			</button>
		</div>
	);
}
