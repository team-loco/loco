export const pageImporters = {
	Dashboard: async () => await import("@/pages/Dashboard"),
	Resources: async () => await import("@/pages/Resources"),
	Resource: async () => await import("@/pages/Resource"),
	NewResource: async () => await import("@/pages/NewResource"),
	Observability: async () => await import("@/pages/Observability"),
	Settings: async () => await import("@/pages/Settings"),
	Team: async () => await import("@/pages/Team"),
	Tokens: async () => await import("@/pages/Tokens"),
	Organizations: async () => await import("@/pages/Organizations"),
	DashboardRedirect: async () => await import("@/pages/DashboardRedirect"),
} as const;
