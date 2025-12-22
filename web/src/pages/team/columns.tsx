import { useState } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { type WorkspaceMemberWithUser } from "@/gen/workspace/v1/workspace_pb";
import { type ColumnDef } from "@tanstack/react-table";
import { Trash2 } from "lucide-react";

const roleBadgeVariants: Record<string, { bg: string; text: string }> = {
	admin: {
		bg: "bg-red-100 dark:bg-red-900",
		text: "text-red-800 dark:text-red-200",
	},
	member: {
		bg: "bg-blue-100 dark:bg-blue-900",
		text: "text-blue-800 dark:text-blue-200",
	},
	viewer: {
		bg: "bg-gray-100 dark:bg-gray-800",
		text: "text-gray-800 dark:text-gray-200",
	},
};

// eslint-disable-next-line react-refresh/only-export-components
function RoleBadge({ role }: { role: string }) {
	const variant =
		roleBadgeVariants[role.toLowerCase()] || roleBadgeVariants.member;
	return (
		<Badge className={`${variant.bg} ${variant.text} capitalize`}>{role}</Badge>
	);
}

function getInitials(name: string): string {
	return name
		.split(" ")
		.map((n) => n[0])
		.join("")
		.toUpperCase()
		.slice(0, 2);
}

interface ActionsCellProps {
	member: WorkspaceMemberWithUser;
	isAdmin: boolean;
	onRemoveMember: (userId: bigint) => void;
	isRemoving: boolean;
}

// eslint-disable-next-line react-refresh/only-export-components
function ActionsCell({
	member,
	isAdmin,
	onRemoveMember,
	isRemoving,
}: ActionsCellProps) {
	const [open, setOpen] = useState(false);

	if (!isAdmin) return null;

	return (
		<div className="flex justify-end">
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="h-8 w-8"
						title="Remove user"
						disabled={isRemoving}
					>
						<Trash2 className="h-4 w-4 text-destructive" />
					</Button>
				</AlertDialogTrigger>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Remove member</AlertDialogTitle>
						<AlertDialogDescription>
							Are you sure you want to remove {member.userName} from the workspace? This
							action cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<div className="flex gap-2 justify-end">
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => {
								onRemoveMember(member.userId);
								setOpen(false);
							}}
							className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
						>
							Remove
						</AlertDialogAction>
					</div>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

export function getColumns(
	isAdmin: boolean,
	onRemoveMember: (userId: bigint) => void,
	isRemoving: boolean
): ColumnDef<WorkspaceMemberWithUser>[] {
	return [
		{
			accessorKey: "userName",
			header: "Display Name",
			cell: ({ row }) => {
				const member = row.original;
				return (
					<div className="flex items-center gap-3">
						<Avatar className="h-8 w-8">
							<AvatarImage src={member.userAvatarUrl} alt={member.userName} />
							<AvatarFallback className="text-xs">
								{getInitials(member.userName)}
							</AvatarFallback>
						</Avatar>
						<span className="font-medium">{member.userName}</span>
					</div>
				);
			},
		},
		{
			accessorKey: "userEmail",
			header: "Email",
			cell: ({ row }) => {
				const member = row.original;
				return (
					<span className="text-muted-foreground">{member.userEmail}</span>
				);
			},
		},
		{
			accessorKey: "role",
			header: "Role",
			cell: ({ row }) => <RoleBadge role={row.getValue("role")} />,
		},
		{
			id: "actions",
			enableHiding: false,
			cell: ({ row }) => (
				<ActionsCell
					member={row.original}
					isAdmin={isAdmin}
					onRemoveMember={onRemoveMember}
					isRemoving={isRemoving}
				/>
			),
		},
	];
}
