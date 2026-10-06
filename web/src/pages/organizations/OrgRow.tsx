import { Building2, Settings, Trash2 } from "lucide-react";
import { useNavigate } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { formatMonthDayYear } from "@/lib/time";

function formatCreated(org: Organization): string {
	const seconds = org.createdAt?.seconds;
	if (seconds === undefined || seconds === 0n) {
		return "Unknown";
	}
	return formatMonthDayYear(Number(seconds) * 1000);
}

export function OrgRow({
	org,
	active,
	onSwitch,
	onDelete,
}: {
	org: Organization;
	active: boolean;
	onSwitch: (orgId: string) => void;
	onDelete: (org: Organization) => void;
}) {
	const navigate = useNavigate();
	const created = formatCreated(org);

	return (
		<div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 border-b border-line px-5 py-3 last:border-b-0 hover:bg-bg2 md:grid-cols-[minmax(0,1fr)_160px_auto]">
			<span className="flex min-w-0 items-center gap-2.5">
				<span className="flex text-fg3">
					<Building2 className="size-4" />
				</span>
				<span className="truncate font-semibold">{org.name}</span>
				{active && (
					<Badge tone="muted" size="sm">
						Current
					</Badge>
				)}
			</span>
			<span className="hidden text-[12.5px] text-fg3 md:block">Created {created}</span>
			<span className="flex items-center gap-1.5">
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						onSwitch(org.id);
					}}
				>
					View
				</Button>
				<Button
					variant="outline"
					size="sm"
					onClick={() => {
						void navigate(`/org/${org.id}/settings`);
					}}
				>
					<Settings />
					Settings
				</Button>
				<Button
					variant="ghost"
					size="icon-sm"
					className="text-fg3 hover:bg-bad-bg hover:text-red"
					aria-label={`Delete ${org.name}`}
					title="Delete organization"
					onClick={() => {
						onDelete(org);
					}}
				>
					<Trash2 />
				</Button>
			</span>
		</div>
	);
}
