import { useTimePreferences } from "@/lib/use-time-preferences";
import { Building2, Copy, Settings, Trash2, ArrowUpRight } from "lucide-react";
import { useNavigate } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";

import { Badge } from "@/components/design/Badge";
import { RowMenu } from "@/components/design/RowMenu";
import { TableCell, TableRow } from "@/components/design/Table";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { useCopy } from "@/hooks/useCopy";
import { formatMonthDayYear, type TimePreferences } from "@/lib/time";

function formatCreated(org: Organization, pref: TimePreferences): string {
	const seconds = org.createdAt?.seconds;
	if (seconds === undefined || seconds === 0n) {
		return "Unknown";
	}
	return formatMonthDayYear(Number(seconds) * 1000, pref);
}

export function OrgRow({
	org,
	active,
	onSwitch,
	role,
}: {
	org: Organization;
	active: boolean;
	onSwitch: (orgId: string) => void;
	role: "Owner" | "Admin" | "Member" | undefined;
}) {
	const navigate = useNavigate();
	const pref = useTimePreferences();
	const created = formatCreated(org, pref);

	const [, copy] = useCopy();
	const settings = `/org/${org.id}/settings`;
	const open = () => {
		onSwitch(org.id);
	};
	return (
		<TableRow
			className="group outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
			role="link"
			tabIndex={0}
			aria-label={`Open ${org.name}`}
			onClick={open}
			onKeyDown={(event) => {
				if (event.target === event.currentTarget && event.key === "Enter") {
					event.preventDefault();
					open();
				}
			}}
		>
			<TableCell className="w-full min-w-0 max-w-0">
				<span className="flex min-w-0 items-center gap-2.5">
					<Building2 className="size-4 shrink-0 text-fg3" />
					<span className="truncate font-semibold text-foreground">{org.name}</span>
					{active && (
						<Badge tone="pill" className="font-mono">
							current
						</Badge>
					)}
				</span>
			</TableCell>
			<TableCell>
				<Badge tone="muted" size="sm">
					{role ?? "…"}
				</Badge>
			</TableCell>
			<TableCell className="hidden text-right text-fg3 min-[701px]:table-cell">
				{org.workspaceCount?.toString() ?? "—"}
			</TableCell>
			<TableCell className="hidden text-right text-fg3 min-[701px]:table-cell">
				{org.memberCount?.toString() ?? "—"}
			</TableCell>
			<TableCell className="hidden whitespace-nowrap text-[12.5px] text-fg3 min-[701px]:table-cell">
				{created}
			</TableCell>
			<TableCell
				onClick={(event) => {
					event.stopPropagation();
				}}
				onKeyDown={(event) => {
					event.stopPropagation();
				}}
			>
				<RowMenu label={`Actions for ${org.name}`}>
					<DropdownMenuItem onClick={open}>
						<ArrowUpRight />
						Open
					</DropdownMenuItem>
					{(role === "Owner" || role === "Admin") && (
						<DropdownMenuItem
							onClick={() => {
								void navigate(settings);
							}}
						>
							<Settings />
							Settings
						</DropdownMenuItem>
					)}
					<DropdownMenuItem
						onClick={() => {
							copy(org.id, org.id, `Copied ${org.id}`);
						}}
					>
						<Copy />
						Copy ID
					</DropdownMenuItem>
					{role === "Owner" && (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuItem
								variant="destructive"
								onClick={() => {
									void navigate(`${settings}#organization-danger`);
								}}
							>
								<Trash2 />
								Delete organization
							</DropdownMenuItem>
						</>
					)}
				</RowMenu>
			</TableCell>
		</TableRow>
	);
}
