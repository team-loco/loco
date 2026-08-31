import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { getServiceSpec } from "@/lib/deployment-utils";

export function shortId(id: string): string {
	if (!id || id.length <= 16) return id;
	return `${id.slice(0, 13)}…`;
}

export function deploymentImage(dep: Deployment): string {
	const svc = getServiceSpec(dep);
	let img = svc?.build?.image ?? "—";
	img = img.replace("registry.gitlab.com/locomotive-group/", "");
	return img;
}

// Parse "500m" → 500, "2" → 2000
export function parseCpuMilli(cpu: string): number {
	if (!cpu) return 0;
	if (cpu.endsWith("m")) return parseInt(cpu, 10);
	return Math.round(parseFloat(cpu) * 1000);
}

// Parse "512Mi" → 512, "1Gi" → 1024, "1.5Gi" → 1536
export function parseMemMi(mem: string): number {
	if (!mem) return 0;
	if (mem.endsWith("Gi")) return Math.round(parseFloat(mem) * 1024);
	if (mem.endsWith("Mi")) return Math.round(parseFloat(mem));
	return Math.round(parseFloat(mem));
}


// ─── Architecture Diagram ─────────────────────────────────────────────────────
