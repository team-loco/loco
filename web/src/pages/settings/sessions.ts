import { SessionKind, type Session } from "@gen/loco/user/v1/user_pb";

const BROWSERS = [
	[/Edg(?:A|iOS)?\//, "Edge"],
	[/(?:Firefox|FxiOS)\//, "Firefox"],
	[/(?:Chrome|CriOS)\//, "Chrome"],
	[/Safari\//, "Safari"],
] as const;
const DEVICES = [
	[/iPhone/, "iPhone"],
	[/iPad/, "iPad"],
	[/Android/, "Android"],
	[/Windows/, "Windows"],
	[/Macintosh/, "macOS"],
	[/Linux/, "Linux"],
] as const;
const SYSTEMS: Record<string, string> = { darwin: "macOS", linux: "Linux", windows: "Windows" };

// The CLI names itself as `loco/v1.2.3 (darwin arm64; build-box)`.
const CLI_AGENT = /^(loco|locoadm)\/(\S+) \((\S+) (\S+); ([^()]+)\)$/;

export interface SessionInfo {
	/** What the session is: `Chrome on macOS`, `Loco CLI on build-box`. */
	label: string;
	/** System and version, such as `macOS arm64 · v1.2.3`; empty when unknown. */
	detail: string;
	ipAddress: string;
}

export function describeSession(session: Pick<Session, "kind" | "userAgent" | "ipAddress">): SessionInfo {
	const { ipAddress } = session;
	if (session.kind === SessionKind.CLI) {
		const match = CLI_AGENT.exec(session.userAgent);
		if (match === null) return { label: "Loco CLI", detail: "", ipAddress };
		const [, product, version = "", os = "", arch = "", host = ""] = match;
		return {
			label: `${product === "locoadm" ? "locoadm" : "Loco CLI"} on ${host}`,
			detail: `${SYSTEMS[os] ?? os} ${arch} · ${version}`,
			ipAddress,
		};
	}
	const browser = BROWSERS.find(([pattern]) => pattern.test(session.userAgent))?.[1] ?? "Dashboard";
	const device = DEVICES.find(([pattern]) => pattern.test(session.userAgent))?.[1];
	return { label: device === undefined ? browser : `${browser} on ${device}`, detail: "", ipAddress };
}

/** A device's sign-in request, read from the User-Agent it sent. */
export function describeDevice(userAgent: string, ipAddress: string): SessionInfo {
	return describeSession({ kind: SessionKind.CLI, userAgent, ipAddress });
}
