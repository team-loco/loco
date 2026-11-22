export const MILLI_PER_CORE = 1000;
const MI_PER_GI = 1024;
export const BYTES_PER_MI = 1_048_576;
const DECIMAL_KILO = 1e3;
const DECIMAL_MEGA = 1e6;
const DECIMAL_GIGA = 1e9;
const CPU_DECIMALS = 3;
const GI_DECIMALS = 1;

export function parseCpuMilli(cpu: string): number {
	if (cpu === "") return 0;
	if (cpu.endsWith("m")) return parseFloat(cpu);
	return parseFloat(cpu) * MILLI_PER_CORE;
}

export function parseMemMi(mem: string): number {
	if (mem === "") return 0;
	if (mem.endsWith("Gi")) return parseFloat(mem) * MI_PER_GI;
	if (mem.endsWith("Mi")) return parseFloat(mem);
	if (mem.endsWith("Ki")) return parseFloat(mem) / MI_PER_GI;
	if (mem.endsWith("G")) return (parseFloat(mem) * DECIMAL_GIGA) / BYTES_PER_MI;
	if (mem.endsWith("M")) return (parseFloat(mem) * DECIMAL_MEGA) / BYTES_PER_MI;
	if (mem.endsWith("k")) return (parseFloat(mem) * DECIMAL_KILO) / BYTES_PER_MI;
	return parseFloat(mem) / BYTES_PER_MI;
}

function trimDecimals(n: number, digits: number): string {
	return n.toFixed(digits).replace(/\.?0+$/, "");
}

export function formatCpu(milli: number): string {
	if (milli > 0 && milli < MILLI_PER_CORE / 10 ** CPU_DECIMALS / 2) return "<0.001 vCPU";
	return `${trimDecimals(milli / MILLI_PER_CORE, CPU_DECIMALS)} vCPU`;
}

export function bytesToMi(bytes: bigint): number {
	return Number(bytes) / BYTES_PER_MI;
}

export function formatMemory(mi: number): string {
	if (mi >= MI_PER_GI) return `${trimDecimals(mi / MI_PER_GI, GI_DECIMALS)} Gi`;
	if (mi > 0 && mi < 0.5) return "<1 Mi";
	return `${Math.round(mi).toString()} Mi`;
}
