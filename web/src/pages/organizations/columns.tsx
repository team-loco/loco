import { useTimePreferences } from "@/lib/use-time-preferences";
import { Building2 } from "lucide-react";
import { useNavigate } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";

import { CellText, type DataColumn } from "@/components/design/DataTable";
import { formatCount } from "@/lib/format";
import { RowMenu } from "@/components/design/RowMenu";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { useCopy } from "@/hooks/useCopy";
import { formatMonthDayYear } from "@/lib/time";

export type OrgRole = "Admin" | "Member" | undefined;

function Created({ org }: { org: Organization }) {
	const pref = useTimePreferences();
	const seconds = org.createdAt?.seconds;
	return <>{seconds === undefined || seconds === 0n ? "Unknown" : formatMonthDayYear(Number(seconds) * 1000, pref)}</>;
}

function OrgMenu({ org, role, onSwitch }: { org: Organization; role: OrgRole; onSwitch: (orgId: string) => void }) {
	const navigate = useNavigate();
	const [, copy] = useCopy();
	const settings = `/org/${org.id}/settings`;
	return (
		<RowMenu label={`Actions for ${org.name}`}>
			<DropdownMenuItem
				onClick={() => {
					onSwitch(org.id);
				}}
			>
				Open
			</DropdownMenuItem>
			{role === "Admin" && (
				<DropdownMenuItem
					onClick={() => {
						void navigate(settings);
					}}
				>
					Settings
				</DropdownMenuItem>
			)}
			<DropdownMenuItem
				onClick={() => {
					copy(org.id, org.id, `Copied ${org.id}`);
				}}
			>
				Copy ID
			</DropdownMenuItem>
			{role === "Admin" && (
				<DropdownMenuItem
					variant="destructive"
					onClick={() => {
						void navigate(`${settings}#organization-danger`);
					}}
				>
					Delete organization
				</DropdownMenuItem>
			)}
		</RowMenu>
	);
}

const COUNT_CELL = "font-mono text-[12.5px]";

function count(value: bigint | undefined): string {
	return value === undefined ? "—" : formatCount(Number(value));
}

export function orgColumns({
	activeOrgId,
	roleOf,
	onSwitch,
}: {
	activeOrgId: string | null;
	roleOf: (orgId: string) => OrgRole;
	onSwitch: (orgId: string) => void;
}): DataColumn<Organization>[] {
	return [
		{
			id: "name",
			header: "Organization",
			size: 140,
			minSize: 120,
			meta: { grow: true, primary: true },
			cell: ({ row: { original: org } }) => (
				<span className="flex min-w-0 items-center gap-2.5">
					<Building2 className="size-4 shrink-0 text-fg3" />
					<CellText text={org.name} className="text-foreground" />
					{org.id === activeOrgId && (
						<span
							data-slot="chip"
							data-chip
							className="inline-flex h-5 shrink-0 items-center rounded-[6px] bg-foreground/7 px-2 font-mono text-[11px] text-fg2"
						>
							current
						</span>
					)}
				</span>
			),
		},
		{
			id: "role",
			header: "Your role",
			size: 110,
			minSize: 90,
			cell: ({ row: { original: org } }) => roleOf(org.id) ?? "…",
		},
		{
			id: "workspaces",
			header: "Workspaces",
			size: 130,
			minSize: 90,
			meta: { hideOnMobile: true, align: "end", cellClassName: COUNT_CELL },
			cell: ({ row: { original: org } }) => count(org.workspaceCount),
		},
		{
			id: "members",
			header: "Members",
			size: 120,
			minSize: 80,
			meta: { hideOnMobile: true, align: "end", cellClassName: COUNT_CELL },
			cell: ({ row: { original: org } }) => count(org.memberCount),
		},
		{
			id: "created",
			header: "Created",
			size: 150,
			minSize: 90,
			meta: { hideOnMobile: true, cellClassName: "whitespace-nowrap text-[12.5px] text-fg3" },
			cell: ({ row: { original: org } }) => <Created org={org} />,
		},
		{
			id: "menu",
			meta: { pinEnd: true, label: "Actions" },
			cell: ({ row: { original: org } }) => <OrgMenu org={org} role={roleOf(org.id)} onSwitch={onSwitch} />,
		},
	];
}
