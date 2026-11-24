export const MILLI_PER_CORE = 1000;
const MI_PER_GI = 1024;
export const BYTES_PER_MI = 1_048_576;
const CPU_DECIMALS = 3;
const GI_DECIMALS = 1;

const QUANTITY_RE = /^([+-]?(?:\d+(?:\.\d*)?|\.\d+))(Ki|Mi|Gi|Ti|Pi|Ei|n|u|m|k|M|G|T|P|E|[eE][+-]?\d+)?$/;

const SUFFIX_FACTOR: Record<string, number> = {
	"": 1,
	Ki: 2 ** 10,
	Mi: 2 ** 20,
	Gi: 2 ** 30,
	Ti: 2 ** 40,
	Pi: 2 ** 50,
	Ei: 2 ** 60,
	n: 1e-9,
	u: 1e-6,
	m: 1e-3,
	k: 1e3,
	M: 1e6,
	G: 1e9,
	T: 1e12,
	P: 1e15,
	E: 1e18,
};

// A Kubernetes quantity in its base unit (cores or bytes); 0 for text that is not one.
function parseQuantity(quantity: string): number {
	const match = QUANTITY_RE.exec(quantity);
	if (!match) return 0;
	const suffix = match[2] ?? "";
	const isExponent = /^[eE][+-]?\d/.test(suffix);
	const factor = isExponent ? 10 ** Number(suffix.slice(1)) : SUFFIX_FACTOR[suffix];
	return Number(match[1]) * (factor ?? 1);
}

export function parseCpuMilli(cpu: string): number {
	return parseQuantity(cpu) * MILLI_PER_CORE;
}

export function parseMemMi(mem: string): number {
	return parseQuantity(mem) / BYTES_PER_MI;
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
