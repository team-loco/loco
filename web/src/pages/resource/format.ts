import type { Timestamp } from "@bufbuild/protobuf/wkt";

export function shortId(id: string): string {
	return id.slice(0, 8);
}

export function tsMillis(ts: Timestamp | undefined): number | undefined {
	if (ts === undefined) return undefined;
	return Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1_000_000);
}

export function imageRef(image: string): string {
	const slash = image.lastIndexOf("/");
	return slash === -1 ? image : image.slice(slash + 1);
}

export function imageTag(image: string): string {
	const ref = imageRef(image);
	const at = ref.indexOf("@");
	if (at !== -1) return ref.slice(at + 1, at + 20);
	const colon = ref.indexOf(":");
	return colon === -1 ? "latest" : ref.slice(colon + 1);
}

export function parseCpuMilli(cpu: string): number {
	if (cpu === "") return 0;
	if (cpu.endsWith("m")) return parseFloat(cpu);
	return parseFloat(cpu) * 1000;
}

export function parseMemMi(mem: string): number {
	if (mem === "") return 0;
	if (mem.endsWith("Gi")) return parseFloat(mem) * 1024;
	if (mem.endsWith("Mi")) return parseFloat(mem);
	if (mem.endsWith("Ki")) return parseFloat(mem) / 1024;
	if (mem.endsWith("G")) return (parseFloat(mem) * 1e9) / 1048576;
	if (mem.endsWith("M")) return (parseFloat(mem) * 1e6) / 1048576;
	return parseFloat(mem) / 1048576;
}

export function fmtCpu(milli: number): string {
	if (milli >= 1000) {
		const cores = (milli / 1000).toFixed(2).replace(/\.?0+$/, "");
		return `${cores} CPU`;
	}
	return `${Math.round(milli).toString()}m`;
}

export function fmtMem(mi: number): string {
	if (mi >= 1024) {
		const gi = (mi / 1024).toFixed(1).replace(/\.0$/, "");
		return `${gi}Gi`;
	}
	return `${Math.round(mi).toString()}Mi`;
}

const startedFormat = new Intl.DateTimeFormat("en-US", {
	month: "short",
	day: "numeric",
	hour: "2-digit",
	minute: "2-digit",
	hourCycle: "h23",
});

export function formatStarted(ms: number | undefined): string {
	if (ms === undefined) return "—";
	return startedFormat.format(ms);
}

function pad(n: number): string {
	return n.toString().padStart(2, "0");
}

export function formatFullTime(ms: number): string {
	const d = new Date(ms);
	const date = `${d.getFullYear().toString()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
	return `${date} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export function formatDuration(ms: number): string {
	const mins = Math.max(0, Math.floor(ms / 60_000));
	const hours = Math.floor(mins / 60);
	const days = Math.floor(hours / 24);
	if (days > 0) {
		const h = hours % 24;
		return h > 0 ? `${days.toString()}d ${h.toString()}h` : `${days.toString()}d`;
	}
	if (hours > 0) {
		const m = mins % 60;
		return m > 0 ? `${hours.toString()}h ${m.toString()}m` : `${hours.toString()}h`;
	}
	return `${Math.max(1, mins).toString()}m`;
}

const QUANTITY = /^\d+(\.\d+)?(m|k|Ki|M|Mi|G|Gi|T|Ti)?$/;

export function isQuantity(value: string): boolean {
	return QUANTITY.test(value.trim());
}
