// Event streaming and subscription helpers
// This module handles WebSocket/SSE subscriptions for real-time updates

export interface WorkspaceEvent {
	id: string;
	appId: string;
	appName: string;
	type:
		| "deployment_started"
		| "deployment_completed"
		| "deployment_failed"
		| "pod_crashed"
		| "scaling"
		| "error"
		| "info";
	message: string;
	severity: "info" | "warning" | "error";
	timestamp: Date;
}

// Callback function for event listeners
export type EventListener = (event: WorkspaceEvent) => void;

// In-memory event store and listener registry
const eventListeners: Map<string, Set<EventListener>> = new Map();
const recentEvents: WorkspaceEvent[] = [];
const MAX_STORED_EVENTS = 50;

/**
 * Subscribe to events for a specific app or workspace
 */
export function subscribeToEvents(
	key: string,
	listener: EventListener
): () => void {
	if (!eventListeners.has(key)) {
		eventListeners.set(key, new Set());
	}
	eventListeners.get(key)!.add(listener);

	// Return unsubscribe function
	return () => {
		const listeners = eventListeners.get(key);
		if (listeners) {
			listeners.delete(listener);
		}
	};
}

/**
 * Emit an event to all listeners
 */
export function emitEvent(event: WorkspaceEvent): void {
	// Store event
	recentEvents.unshift(event);
	if (recentEvents.length > MAX_STORED_EVENTS) {
		recentEvents.pop();
	}

	// Notify workspace listeners
	const workspaceListeners = eventListeners.get("workspace");
	if (workspaceListeners) {
		workspaceListeners.forEach((listener) => listener(event));
	}

	// Notify app-specific listeners
	const appListeners = eventListeners.get(`app:${event.appId}`);
	if (appListeners) {
		appListeners.forEach((listener) => listener(event));
	}
}

/**
 * Get recent events
 */
export function getRecentEvents(limit: number = 10): WorkspaceEvent[] {
	return recentEvents.slice(0, limit);
}
