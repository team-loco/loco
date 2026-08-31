import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { getServiceSpec } from "@/lib/deployment-utils";
import { relativeTime } from "@/lib/format-time";

export interface DiffRow {
	key: string;
	label: string;
	current: string;
	old: string;
	changed: boolean;
}

export function buildDiff(current: Deployment, old: Deployment): DiffRow[] {
	const rows: DiffRow[] = [];
	const cs = getServiceSpec(current);
	const os = getServiceSpec(old);
	if (!cs || !os) return rows;

	const row = (
		key: string,
		label: string,
		cv: string | number | undefined,
		ov: string | number | undefined,
	) => {
		const c = String(cv ?? "—"),
			o = String(ov ?? "—");
		rows.push({ key, label, current: c, old: o, changed: c !== o });
	};

	row("image", "Image", cs.build?.image, os.build?.image);
	row("cpu", "CPU limit", cs.cpu, os.cpu);
	row("memory", "Memory limit", cs.memory, os.memory);
	row("minReplicas", "Min replicas", cs.minReplicas, os.minReplicas);
	row("maxReplicas", "Max replicas", cs.maxReplicas, os.maxReplicas);
	row("port", "Port", cs.port, os.port);

	const SENSITIVE = [
		"DATABASE_URL",
		"STRIPE_KEY",
		"REDIS_URL",
		"SECRET",
		"PASSWORD",
		"TOKEN",
	];
	const csEnv = cs.env,
		osEnv = os.env;
	const allKeys = new Set([...Object.keys(csEnv), ...Object.keys(osEnv)]);
	allKeys.forEach((k) => {
		const cv = csEnv[k],
			ov = osEnv[k];
		if (cv !== ov) {
			const mask = SENSITIVE.some((s) => k.toUpperCase().includes(s));
			rows.push({
				key: `env_${k}`,
				label: `env.${k}`,
				current: cv !== undefined ? (mask ? "••••••" : cv) : "—",
				old: ov !== undefined ? (mask ? "••••••" : ov) : "—",
				changed: true,
			});
		}
	});
	return rows;
}

export function SpecDiffModal({
	current,
	old,
	onClose,
}: {
	current: Deployment;
	old: Deployment;
	onClose: () => void;
}) {
	const rows = buildDiff(current, old);
	const changed = rows.filter((r) => r.changed);
	const same = rows.filter((r) => !r.changed);

	return (
		<div
			className="fixed inset-0 z-50 flex items-center justify-center bg-[rgba(42,32,24,0.3)] backdrop-blur-[3px]"
			onClick={onClose}
		>
			<div
				className="bg-card rounded-2xl border border-[#e0d8cc] shadow-[0_20px_60px_rgba(42,32,24,0.2)] w-[min(640px,calc(100vw-48px))] max-h-[80vh] overflow-hidden flex flex-col"
				onClick={(e) => {
					e.stopPropagation();
				}}
			>
				<div className="px-6 pt-5 pb-4 border-b border-[#ede7dd] flex items-start justify-between gap-4">
					<div>
						<div className="font-serif text-[18px] mb-[6px]">Spec diff</div>
						<div className="text-[12px] text-[#a0907e] flex gap-2 items-center flex-wrap">
							<span className="font-mono bg-[#ede7dd] px-[7px] py-[2px] rounded">
								{relativeTime(old.createdAt)}
							</span>
							<span className="text-[#c4956a]">→</span>
							<span className="font-mono bg-[#eaf2ed] text-[#3a6b4a] px-[7px] py-[2px] rounded">
								{relativeTime(current.createdAt)} (current)
							</span>
						</div>
					</div>
					<button
						onClick={onClose}
						className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 shrink-0"
					>
						<svg
							width="18"
							height="18"
							viewBox="0 0 24 24"
							fill="none"
							stroke="currentColor"
							strokeWidth="2"
						>
							<line x1="18" y1="6" x2="6" y2="18" />
							<line x1="6" y1="6" x2="18" y2="18" />
						</svg>
					</button>
				</div>
				<div className="overflow-y-auto px-6 pb-6 pt-4">
					{changed.length === 0 ? (
						<div className="text-center py-8 text-[#a0907e] text-sm">
							No spec changes between these deployments.
						</div>
					) : (
						<>
							<div className="text-[11px] font-semibold text-[#8b2e2e] tracking-[0.07em] uppercase mb-2">
								{changed.length} change{changed.length !== 1 ? "s" : ""}
							</div>
							<div className="flex flex-col gap-1 mb-5">
								{changed.map((r) => (
									<div
										key={r.key}
										className="grid grid-cols-[140px_1fr_1fr] gap-2 p-[9px_12px] bg-[#fff8f0] rounded-lg border border-[#f0ddc8] items-start"
									>
										<span className="font-mono text-[11px] text-[#8a7a68] pt-3.5">
											{r.label}
										</span>
										<div className="flex flex-col gap-0.5">
											<span className="text-[9px] text-[#c0392b] font-semibold tracking-[0.06em] uppercase">
												Before
											</span>
											<span className="font-mono text-[11px] text-[#8b2e2e] bg-[#fdeaea] px-[7px] py-[3px] rounded-[5px] break-all">
												{r.old}
											</span>
										</div>
										<div className="flex flex-col gap-0.5">
											<span className="text-[9px] text-[#3a6b4a] font-semibold tracking-[0.06em] uppercase">
												After
											</span>
											<span className="font-mono text-[11px] text-[#3a6b4a] bg-[#eaf2ed] px-[7px] py-[3px] rounded-[5px] break-all">
												{r.current}
											</span>
										</div>
									</div>
								))}
							</div>
						</>
					)}
					{same.length > 0 && (
						<>
							<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.07em] uppercase mb-2">
								Unchanged
							</div>
							<div className="flex flex-col gap-[3px]">
								{same.map((r) => (
									<div
										key={r.key}
										className="grid grid-cols-[140px_1fr] gap-2 p-[7px_12px] rounded-[7px] items-center"
									>
										<span className="font-mono text-[11px] text-[#b0a090]">
											{r.label}
										</span>
										<span className="font-mono text-[11px] text-[#8a7a68]">
											{r.current}
										</span>
									</div>
								))}
							</div>
						</>
					)}
				</div>
			</div>
		</div>
	);
}

// ─── Activity Sheet ───────────────────────────────────────────────────────────
