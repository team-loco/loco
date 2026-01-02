import { useLocation, useNavigate } from "react-router";
import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbLink,
	BreadcrumbList,
	BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";

interface BreadcrumbSegment {
	label: string;
	href?: string;
}

export function BreadcrumbNav() {
	const location = useLocation();
	const navigate = useNavigate();

	const getBreadcrumbs = (): BreadcrumbSegment[] => {
		const pathname = location.pathname;

		// Root/Dashboard
		if (pathname === "/" || pathname === "/dashboard") {
			return [{ label: "Dashboard" }];
		}

		// App details: /app/:appId
		const appMatch = pathname.match(/^\/app\/([^/]+)(?:\/settings)?$/);
		if (appMatch) {
			const appId = appMatch[1];
			const isSettings = pathname.endsWith("/settings");
			const breadcrumbs: BreadcrumbSegment[] = [
				{ label: "Dashboard", href: "/" },
				{ label: "App", href: `/app/${appId}` },
			];
			if (isSettings) {
				breadcrumbs.push({ label: "Settings" });
			}
			return breadcrumbs;
		}

		// Org settings: /org/:orgId/settings
		const orgMatch = pathname.match(/^\/org\/([^/]+)\/settings$/);
		if (orgMatch) {
			return [
				{ label: "Dashboard", href: "/" },
				{ label: "Organization Settings" },
			];
		}

		// Workspace settings: /workspace/:workspaceId/settings
		const wsMatch = pathname.match(/^\/workspace\/([^/]+)\/settings$/);
		if (wsMatch) {
			return [
				{ label: "Dashboard", href: "/" },
				{ label: "Workspace Settings" },
			];
		}

		// Profile: /profile
		if (pathname === "/profile") {
			return [{ label: "Dashboard", href: "/" }, { label: "Profile" }];
		}

		// Create app: /create-resource
		if (pathname === "/create-resource") {
			return [{ label: "Dashboard", href: "/" }, { label: "Create App" }];
		}

		// Default fallback
		return [{ label: "Dashboard", href: "/" }];
	};

	const breadcrumbs = getBreadcrumbs();

	if (breadcrumbs.length === 0) {
		return null;
	}

	return (
		<Breadcrumb>
			<BreadcrumbList>
				{breadcrumbs.map((segment, idx) => (
					<div key={idx} className="flex items-center gap-2">
						<BreadcrumbItem>
							{segment.href ? (
								<BreadcrumbLink
									onClick={() => navigate(segment.href!)}
									className="cursor-pointer"
								>
									{segment.label}
								</BreadcrumbLink>
							) : (
								<span className="text-foreground">{segment.label}</span>
							)}
						</BreadcrumbItem>
						{idx < breadcrumbs.length - 1 && <BreadcrumbSeparator />}
					</div>
				))}
			</BreadcrumbList>
		</Breadcrumb>
	);
}
