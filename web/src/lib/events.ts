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

/**
 * Mock event generation for testing
 * TODO: Replace with real WebSocket/SSE implementation
 */
export function initializeMockEvents(): void {
	// Generate mock events periodically for demo purposes
	setInterval(() => {
		const eventTypes = [
			"deployment_started",
			"deployment_completed",
			"pod_crashed",
			"scaling",
		] as const;
		const appNames = ["API", "Web", "Worker", "DB"];

		if (Math.random() > 0.7) {
			// 30% chance of new event
			const randomType =
				eventTypes[Math.floor(Math.random() * eventTypes.length)];
			const randomApp = appNames[Math.floor(Math.random() * appNames.length)];

			emitEvent({
				id: `evt-${Date.now()}-${Math.random()}`,
				appId: `app-${randomApp.toLowerCase()}`,
				appName: randomApp,
				type: randomType,
				message: getEventMessage(randomType, randomApp),
				severity: randomType === "pod_crashed" ? "error" : "info",
				timestamp: new Date(),
			});
		}
	}, 5000); // Check every 5 seconds
}

function getEventMessage(type: string, appName: string): string {
	const messages: Record<string, string> = {
		deployment_started: `${appName} deployment started`,
		deployment_completed: `${appName} deployment completed successfully`,
		deployment_failed: `${appName} deployment failed`,
		pod_crashed: `${appName} pod crashed and restarted`,
		scaling: `${appName} scaling in progress`,
	};
	return messages[type] || "Event occurred";
}

/**
 * Setup event listeners for real-time updates
 * This will connect to backend WebSocket/SSE when available
 */
export function setupEventStreaming(workspaceId: string): () => void {
	// TODO: Replace with actual WebSocket/SSE connection
	console.log(`[Events] Setting up streaming for workspace: ${workspaceId}`);

	// Return cleanup function
	return () => {
		console.log(`[Events] Cleaning up streaming for workspace: ${workspaceId}`);
	};
}
