import { Scope } from "@gen/loco/token/v1/token_pb";

import { Badge } from "@/components/design/Badge";
import { cn } from "@/lib/utils";

export type ScopeName = "read" | "write" | "admin";

export const SCOPE_NAMES: ScopeName[] = ["read", "write", "admin"];

export function toScopeName(s: Scope): ScopeName | null {
	switch (s) {
		case Scope.READ:
			return "read";
		case Scope.WRITE:
			return "write";
		case Scope.ADMIN:
			return "admin";
		case Scope.UNSPECIFIED:
			return null;
	}
}

export function toScope(s: ScopeName): Scope {
	switch (s) {
		case "read":
			return Scope.READ;
		case "write":
			return Scope.WRITE;
		case "admin":
			return Scope.ADMIN;
	}
}

export function parseScopeName(raw: string): ScopeName | null {
	switch (raw) {
		case "read":
			return "read";
		case "write":
			return "write";
		case "admin":
			return "admin";
		default:
			return null;
	}
}

export function sortScopes(list: ScopeName[]): ScopeName[] {
	return SCOPE_NAMES.filter((s) => list.includes(s));
}

export function scopeAllows(s: ScopeName): string {
	switch (s) {
		case "read":
			return "List resources, environments and members";
		case "write":
			return "Create resources and environments, add members";
		case "admin":
			return "Delete the workspace or environments, remove members";
	}
}

function scopeClass(s: ScopeName): string {
	switch (s) {
		case "read":
			return "bg-bg3 text-fg2";
		case "write":
			return "bg-info-bg text-info-fg";
		case "admin":
			return "bg-warn-bg text-warn-fg";
	}
}

export function ScopeBadge({ scope, className }: { scope: ScopeName; className?: string | undefined }) {
	return (
		<Badge size="sm" className={cn("font-semibold", scopeClass(scope), className)}>
			{scope}
		</Badge>
	);
}
