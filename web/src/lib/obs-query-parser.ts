export interface ParsedQuery {
	regions?: string[];
	excludedRegions?: string[];
	search: string;
	levels: string[];
	labels: Record<string, string>;
}
