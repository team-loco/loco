export const pageImporters = {
	Dashboard: async () => await import("@/pages/Dashboard"),
	Resource: async () => await import("@/pages/Resource"),
	Observability: async () => await import("@/pages/Observability"),
	Settings: async () => await import("@/pages/Settings"),
	Team: async () => await import("@/pages/Team"),
	Tokens: async () => await import("@/pages/Tokens"),
	Organizations: async () => await import("@/pages/Organizations"),
	Profile: async () => await import("@/pages/Profile"),
	DashboardRedirect: async () => await import("@/pages/DashboardRedirect"),
} as const;
