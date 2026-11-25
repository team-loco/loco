interface ObservabilityScope {
	workspaceId: string;
	environmentId: string;
	/** Reads Loco's own telemetry, which has no workspace or environment. */
	installation?: boolean | undefined;
}

/** Whether a query can run: the installation scope needs no workspace or environment. */
export function scopeReady(scope: ObservabilityScope, anyEnvironment = false): boolean {
	if (scope.installation === true) return true;
	return scope.workspaceId !== "" && (anyEnvironment || scope.environmentId !== "");
}
