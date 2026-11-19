const PLACEHOLDER_RE = /\{([^{}\s]+)\}/g;

/** The body of a structured log with each {name} placeholder replaced by the log attribute name. */
export function renderTemplate(body: string, attributes: Record<string, string>): string {
	return body.replace(PLACEHOLDER_RE, (placeholder, name: string) =>
		Object.hasOwn(attributes, name) ? (attributes[name] ?? placeholder) : placeholder,
	);
}
