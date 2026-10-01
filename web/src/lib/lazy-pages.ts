export const pageImporters = {
	ResourceDetails: async () => await import("@/pages/ResourceDetails"),
	ResourceSettings: async () => await import("@/pages/ResourceSettings"),
	CreateResource: async () => await import("@/pages/CreateResource"),
	Events: async () => await import("@/pages/Events"),
	Home: async () => await import("@/pages/Home"),
	Organizations: async () => await import("@/pages/Organizations"),
	OrgSettings: async () => await import("@/pages/OrgSettings"),
	Profile: async () => await import("@/pages/Profile"),
	Team: async () => await import("@/pages/Team"),
	Tokens: async () => await import("@/pages/Tokens"),
	WorkspaceSettings: async () => await import("@/pages/WorkspaceSettings"),
	Observability: async () => await import("@/pages/Observability"),
	Resources: async () => await import("@/pages/Resources"),
	Usage: async () => await import("@/pages/Usage"),
	DashboardRedirect: async () => await import("@/pages/DashboardRedirect"),
} as const;

const BY_SEGMENT: Record<string, keyof typeof pageImporters> = {
	dashboard: "Home",
	observability: "Observability",
	events: "Events",
	usage: "Usage",
	tokens: "Tokens",
	team: "Team",
	resources: "Resources",
	"create-resource": "CreateResource",
	settings: "WorkspaceSettings",
	profile: "Profile",
	organizations: "Organizations",
};

const started = new Set<string>();

export function prefetchRoute(url: string): void {
	const segment = url.split("?")[0]?.split("/").filter(Boolean).pop();
	if (segment === undefined) return;
	const key = BY_SEGMENT[segment];
	if (key === undefined || started.has(key)) return;
	started.add(key);
	void pageImporters[key]().catch(() => {
		started.delete(key);
	});
}
