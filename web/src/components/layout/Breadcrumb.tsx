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

		// Resource details: /resource/:resourceId
		const resourceMatch = /^\/resource\/([^/]+)(?:\/settings)?$/.exec(pathname);
		if (resourceMatch) {
			const resourceId = resourceMatch[1];
			const isSettings = pathname.endsWith("/settings");
			const breadcrumbs: BreadcrumbSegment[] = [
				{ label: "Dashboard", href: "/" },
				{ label: "Resource", href: `/resource/${resourceId}` },
			];
			if (isSettings) {
				breadcrumbs.push({ label: "Settings" });
			}
			return breadcrumbs;
		}

		// Org settings: /org/:orgId/settings
		const orgMatch = /^\/org\/([^/]+)\/settings$/.exec(pathname);
		if (orgMatch) {
			return [
				{ label: "Dashboard", href: "/" },
				{ label: "Organization Settings" },
			];
		}

		// Workspace settings: /workspace/:workspaceId/settings
		const wsMatch = /^\/workspace\/([^/]+)\/settings$/.exec(pathname);
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

		// Create resource: /create-resource
		if (pathname === "/create-resource") {
			return [{ label: "Dashboard", href: "/" }, { label: "Create Resource" }];
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
				{breadcrumbs.map((segment) => (
					<div key={segment.label} className="flex items-center gap-2">
						<BreadcrumbItem>
							{segment.href ? (
								<BreadcrumbLink
									onClick={() => {
										void navigate(segment.href);
									}}
									className="cursor-pointer"
								>
									{segment.label}
								</BreadcrumbLink>
							) : (
								<span className="text-foreground">{segment.label}</span>
							)}
						</BreadcrumbItem>
						{segment.href && <BreadcrumbSeparator />}
					</div>
				))}
			</BreadcrumbList>
		</Breadcrumb>
	);
}
