import { useQuery } from "@connectrpc/connect-query";
import { listClusters } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import { getOrg } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { Link, matchPath, useLocation } from "react-router";

import { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbSeparator } from "@/components/design/Breadcrumb";
import { SYSTEM_OBSERVABILITY_VIEWS } from "@/lib/routes";
import {
	atPath,
	CLUSTERS_PATH,
	NEW_CLUSTER_PATH,
	OPERATORS_PATH,
	ORGS_PATH,
	SYSTEM_AUDIT_PATH,
	SYSTEM_PATH,
	SYSTEM_VIEW_TITLE,
	USERS_PATH,
} from "@/lib/system";

const PEOPLE_PAGES: Record<string, string> = { [OPERATORS_PATH]: "Operators", [USERS_PATH]: "Users" };

const CRUMB = "flex h-6.5 items-center rounded-[5px] px-1.5 font-mono text-[12.5px] whitespace-nowrap";

function Slash() {
	return <BreadcrumbSeparator className="px-0.5 text-fg3 max-[760px]:px-0">/</BreadcrumbSeparator>;
}

function Crumb({ to, children }: { to?: string; children: string }) {
	return (
		<BreadcrumbItem className="min-w-0">
			{to === undefined ? (
				<span className={`${CRUMB} truncate text-foreground`}>{children}</span>
			) : (
				<Link
					to={to}
					className={`${CRUMB} text-fg3 outline-none transition-colors hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring/50`}
				>
					{children}
				</Link>
			)}
		</BreadcrumbItem>
	);
}

/** Every System page starts with the dark System tag, so it is clear the page is not an organization's. */
export function SystemBreadcrumb() {
	const { pathname } = useLocation();
	const clusterId = matchPath(`${CLUSTERS_PATH}/:clusterId`, pathname)?.params.clusterId;
	const adding = atPath(pathname, NEW_CLUSTER_PATH, true);
	const detail = clusterId !== undefined && !adding;
	const clusters = useQuery(listClusters, {}, { enabled: detail });
	const name = clusters.data?.clusters.find((c) => c.id === clusterId)?.name ?? clusterId ?? "";
	const onClusters = atPath(pathname, CLUSTERS_PATH);
	const onAudit = atPath(pathname, SYSTEM_AUDIT_PATH);
	const orgId = matchPath(`${ORGS_PATH}/:orgId`, pathname)?.params.orgId;
	const org = useQuery(getOrg, { orgId: orgId ?? "" }, { enabled: orgId !== undefined });
	const onOrgs = atPath(pathname, ORGS_PATH);
	const view = SYSTEM_OBSERVABILITY_VIEWS.find((v) => atPath(pathname, `${SYSTEM_PATH}/${v}`));
	const page = view === undefined ? (PEOPLE_PAGES[pathname] ?? "Overview") : SYSTEM_VIEW_TITLE[view];
	return (
		<Breadcrumb className="min-w-0 flex-auto overflow-hidden min-[761px]:flex-1 min-[761px]:basis-0">
			<BreadcrumbList>
				<BreadcrumbItem>
					<Link
						to={SYSTEM_PATH}
						data-slot="system-tag"
						className="flex h-5.5 items-center rounded-[5px] bg-foreground px-2 text-[11.5px] font-semibold tracking-[0.02em] text-background uppercase outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
					>
						System
					</Link>
				</BreadcrumbItem>
				<Slash />
				{onAudit && <Crumb>Audit log</Crumb>}
				{!onClusters && !onOrgs && !onAudit && <Crumb>{page}</Crumb>}
				{onClusters && (
					<>
						<Crumb {...(clusterId === undefined ? {} : { to: CLUSTERS_PATH })}>Clusters</Crumb>
						{adding && (
							<>
								<Slash />
								<Crumb>Add</Crumb>
							</>
						)}
						{detail && (
							<>
								<Slash />
								<Crumb>{name}</Crumb>
							</>
						)}
					</>
				)}
				{onOrgs && (
					<>
						<Crumb {...(orgId === undefined ? {} : { to: ORGS_PATH })}>Organizations</Crumb>
						{orgId !== undefined && (
							<>
								<Slash />
								<Crumb>{org.data?.organization?.name ?? orgId}</Crumb>
							</>
						)}
					</>
				)}
			</BreadcrumbList>
		</Breadcrumb>
	);
}
