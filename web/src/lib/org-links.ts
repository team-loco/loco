const ORG_ID_PLACEHOLDER = "{orgId}";

export function expandOrgLink(template: string, orgId: string): string {
	return template.replaceAll(ORG_ID_PLACEHOLDER, encodeURIComponent(orgId));
}
