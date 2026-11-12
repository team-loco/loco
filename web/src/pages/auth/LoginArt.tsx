import { cn } from "@/lib/utils";

const REDUCED_MOTION = "(prefers-reduced-motion: reduce)";
const FRAME_MS = 33;
const STILL_FRAME_MS = 250;
const STILL_T = 2.2;
const STEP = 9;
const TAU = Math.PI * 2;

interface Band {
	base: number;
	slope: number;
	amp: number;
	f: number;
	sp: number;
	ph: number;
	w: number;
	wv: number;
	gain: number;
}

const BANDS: Band[] = [
	{ base: 0.42, slope: -0.34, amp: 0.07, f: 4.2, sp: 0.42, ph: 0, w: 0.055, wv: 0.02, gain: 1 },
	{ base: 0.66, slope: -0.3, amp: 0.09, f: 3.1, sp: -0.33, ph: 1.7, w: 0.085, wv: 0.03, gain: 0.85 },
	{ base: 0.9, slope: -0.38, amp: 0.05, f: 5.6, sp: 0.55, ph: 3.1, w: 0.035, wv: 0.012, gain: 0.9 },
];
// [band, position along the band]
const MARKS: [number, number][] = [
	[0, 0.27],
	[0, 0.71],
	[2, 0.5],
];
// [band, phase, speed]
const PACKETS: [number, number, number][] = [
	[0, 0, 0.055],
	[0, 0.48, 0.055],
	[2, 0.2, 0.07],
	[2, 0.7, 0.07],
];
// Each band takes its colour from a hidden swatch, so the art follows the colour theme.
const SWATCHES = [
	"var(--brand)",
	"color-mix(in oklab, var(--brand) 55%, var(--err))",
	"color-mix(in oklab, var(--brand) 45%, var(--ok))",
];

const gauss = (d: number, w: number) => Math.exp(-(d * d) / (2 * w * w));

function band(k: number): Band {
	const b = BANDS[k];
	if (b === undefined) throw new Error(`unknown band ${String(k)}`);
	return b;
}

function start(aside: HTMLElement): () => void {
	const canvas = aside.querySelector("canvas");
	const ctx = canvas?.getContext("2d");
	if (!canvas || !ctx) return () => undefined;
	const swatches = [...aside.querySelectorAll<HTMLElement>("[data-swatch]")].map((el) => getComputedStyle(el));
	const host = getComputedStyle(aside);
	const reduce = window.matchMedia(REDUCED_MOTION);
	let w = 0;
	let h = 0;
	let dpr = 1;
	const size = () => {
		const r = canvas.getBoundingClientRect();
		dpr = Math.min(2, window.devicePixelRatio);
		w = r.width;
		h = r.height;
		canvas.width = Math.round(w * dpr);
		canvas.height = Math.round(h * dpr);
	};
	size();
	const ro = new ResizeObserver(size);
	ro.observe(canvas);

	// The pointer pulls the bands toward it and lights a lens around it, eased so it trails the cursor.
	const m = { x: 0.5, y: 0.5, tx: 0.5, ty: 0.5, s: 0, ts: 0 };
	const onMove = (e: PointerEvent) => {
		const r = canvas.getBoundingClientRect();
		m.tx = (e.clientX - r.left) / r.width;
		m.ty = (e.clientY - r.top) / r.height;
		if (m.ts === 0) {
			m.x = m.tx;
			m.y = m.ty;
		}
		m.ts = 1;
	};
	const onLeave = () => {
		m.ts = 0;
	};
	aside.addEventListener("pointermove", onMove);
	aside.addEventListener("pointerleave", onLeave);

	const raw = (k: number, u: number, t: number) => {
		const b = band(k);
		return b.base + b.slope * u + b.amp * Math.sin(u * b.f + t * b.sp + b.ph) + 0.02 * Math.sin(u * 11 - t * 0.8 + k);
	};
	const centre = (k: number, u: number, t: number) => {
		const c = raw(k, u, t);
		if (m.s < 0.01) return c;
		return c + (m.y - c) * 0.45 * gauss(u - m.x, 0.13) * gauss(c - m.y, 0.22) * m.s;
	};

	let raf = 0;
	let last = 0;
	const draw = (now: number) => {
		raf = requestAnimationFrame(draw);
		const still = reduce.matches;
		if (now - last < (still ? STILL_FRAME_MS : FRAME_MS)) return;
		last = now;
		const t = still ? STILL_T : now / 1000;
		if (still) m.s = 0;
		else {
			m.x += (m.tx - m.x) * 0.12;
			m.y += (m.ty - m.y) * 0.12;
			m.s += (m.ts - m.s) * 0.08;
		}
		const colors = swatches.map((s) => s.color);
		const ink = host.color;
		const bg = host.backgroundColor;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, w, h);
		const packets = still
			? []
			: PACKETS.map(([k, ph, sp]) => {
					const u = ((t * sp + ph) % 1.15) - 0.05;
					return { k, u, x: u * w, y: centre(k, u, t) * h };
				}).filter((p) => p.u > -0.02 && p.u < 1.02);
		const mx = m.x * w;
		const my = m.y * h;
		const lensR = Math.min(w, h) * 0.12;
		for (let x = STEP / 2; x < w; x += STEP) {
			const u = x / w;
			const cs = BANDS.map((_, k) => centre(k, u, t));
			const ws = BANDS.map((b, k) => b.w + b.wv * Math.sin(u * 3.3 + t * 0.3 + k));
			const pulse = BANDS.map((_, k) => 0.7 + 0.3 * Math.sin(u * 5 - t * 0.5 + k * 2));
			for (let y = STEP / 2; y < h; y += STEP) {
				const v = y / h;
				const is = BANDS.map((b, k) => gauss(v - (cs[k] ?? 0), ws[k] ?? 1) * (pulse[k] ?? 1) * b.gain);
				for (const p of packets) {
					const d2 = (x - p.x) ** 2 + (y - p.y) ** 2;
					if (d2 < 900) is[p.k] = (is[p.k] ?? 0) + Math.exp(-d2 / 260) * 0.9;
				}
				const sum = is.reduce((a, b) => a + b, 0);
				const edge = Math.min(1, u * 5, (1 - u) * 5, v * 5, (1 - v) * 5);
				let intensity = Math.min(1, sum) * edge;
				if (m.s > 0.01) {
					const lens = Math.exp(-((x - mx) ** 2 + (y - my) ** 2) / (2 * lensR * lensR)) * m.s;
					intensity = Math.min(1, intensity + lens * (0.25 + 0.6 * intensity));
				}
				const strongest = is.indexOf(Math.max(...is));
				const r = 0.5 + 2.4 * intensity ** 1.15;
				ctx.globalAlpha = 0.1 + 0.86 * intensity;
				ctx.fillStyle = colors[sum > 0.001 ? strongest : 1] ?? ink;
				if (r < 1.15) ctx.fillRect(x - r, y - r, r * 2, r * 2);
				else {
					ctx.beginPath();
					ctx.arc(x, y, r, 0, TAU);
					ctx.fill();
				}
			}
		}
		ctx.strokeStyle = ink;
		ctx.lineWidth = 1;
		for (const [k, alpha, dash] of [
			[0, 0.28, []],
			[2, 0.2, [3, 4]],
		] as const) {
			ctx.globalAlpha = alpha;
			ctx.setLineDash(dash);
			ctx.beginPath();
			for (let px = 0; px <= w; px += 5) {
				const yy = centre(k, px / w, t) * h;
				if (px === 0) ctx.moveTo(px, yy);
				else ctx.lineTo(px, yy);
			}
			ctx.stroke();
		}
		ctx.setLineDash([]);
		for (const p of packets) {
			const color = colors[p.k] ?? ink;
			ctx.strokeStyle = color;
			ctx.lineWidth = 2;
			for (let s = 0; s < 14; s++) {
				const u0 = p.u - s * 0.006;
				const u1 = p.u - (s + 1) * 0.006;
				ctx.globalAlpha = 0.85 * (1 - s / 14);
				ctx.beginPath();
				ctx.moveTo(u0 * w, centre(p.k, u0, t) * h);
				ctx.lineTo(u1 * w, centre(p.k, u1, t) * h);
				ctx.stroke();
			}
			ctx.fillStyle = color;
			ctx.globalAlpha = 0.18;
			ctx.beginPath();
			ctx.arc(p.x, p.y, 6, 0, TAU);
			ctx.fill();
			ctx.globalAlpha = 1;
			ctx.beginPath();
			ctx.arc(p.x, p.y, 2.6, 0, TAU);
			ctx.fill();
		}
		// Markers light up as a packet passes them.
		ctx.lineWidth = 1;
		MARKS.forEach(([k, at], j) => {
			const u = at + (still ? 0 : 0.012 * Math.sin(t * 0.45 + j));
			const xx = u * w;
			const yy = centre(k, u, t) * h;
			const hit = packets.reduce(
				(acc, p) => (p.k === k ? Math.max(acc, Math.exp(-(((p.u - u) / 0.025) ** 2))) : acc),
				0,
			);
			const color = colors[k] ?? ink;
			const sz = 3.5 + hit * 1.5;
			if (hit > 0.05) {
				ctx.globalAlpha = 0.16 * hit;
				ctx.fillStyle = color;
				ctx.beginPath();
				ctx.arc(xx, yy, 10 + hit * 6, 0, TAU);
				ctx.fill();
			}
			ctx.globalAlpha = hit > 0.3 ? 1 : 0.6;
			ctx.fillStyle = hit > 0.3 ? color : bg;
			ctx.strokeStyle = hit > 0.3 ? color : ink;
			ctx.fillRect(xx - sz, yy - sz, sz * 2, sz * 2);
			ctx.strokeRect(xx - sz, yy - sz, sz * 2, sz * 2);
			ctx.globalAlpha = 0.32 + hit * 0.3;
			ctx.strokeStyle = ink;
			ctx.beginPath();
			ctx.moveTo(xx, yy - 9 - hit * 2);
			ctx.lineTo(xx, yy - 18 - hit * 4);
			ctx.moveTo(xx, yy + 9 + hit * 2);
			ctx.lineTo(xx, yy + 18 + hit * 4);
			ctx.stroke();
		});
		ctx.globalAlpha = 1;
	};
	raf = requestAnimationFrame(draw);

	return () => {
		cancelAnimationFrame(raf);
		ro.disconnect();
		aside.removeEventListener("pointermove", onMove);
		aside.removeEventListener("pointerleave", onLeave);
	};
}

/** Halftone bands that carry packets between markers and bend toward the pointer; still under reduced motion. */
export function LoginArt({ className }: { className?: string }) {
	return (
		<aside
			ref={start}
			aria-hidden="true"
			className={cn(
				"relative m-2 min-h-[calc(100dvh-16px)] cursor-crosshair overflow-hidden rounded-2xl bg-panel text-foreground shadow-[inset_0_0_0_1px_var(--line)]",
				className,
			)}
		>
			{SWATCHES.map((color) => (
				<span key={color} data-swatch hidden style={{ color }} />
			))}
			<canvas className="absolute inset-0 block size-full" />
		</aside>
	);
}
