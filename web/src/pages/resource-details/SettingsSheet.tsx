import { Input } from "@/components/design/Input";
import { Slider } from "@/components/ui/slider";
import { updateResourceDomain } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import type { ResourceDomain } from "@gen/loco/domain/v1/domain_pb";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { deleteResource, scaleResource, updateResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { getServiceSpec } from "@/lib/deployment-utils";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { cn } from "@/lib/utils";
import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

export const CPU_OPTIONS = [
	"100m",
	"250m",
	"500m",
	"750m",
	"1000m",
	"1250m",
	"1500m",
	"1750m",
	"2000m",
];
export const DEFAULT_CPU = "500m";
export const MEM_OPTIONS = [
	"256Mi",
	"512Mi",
	"768Mi",
	"1Gi",
	"1.25Gi",
	"1.5Gi",
	"2Gi",
];
export const DEFAULT_MEM = "512Mi";

export interface SettingsSheetProps {
	open: boolean;
	onClose: () => void;
	resourceId: string;
	resourceName: string;
	domains: ResourceDomain[];
	activeDep: Deployment | undefined;
}

export function SettingsSheet({
	open,
	onClose,
	resourceId,
	resourceName: initialName,
	domains,
	activeDep,
}: SettingsSheetProps) {
	const navigate = useNavigate();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	// Scale state
	const svc = activeDep ? getServiceSpec(activeDep) : undefined;
	const initCpuIdx = svc?.cpu ? CPU_OPTIONS.indexOf(svc.cpu) : -1;
	const initMemIdx = svc?.memory ? MEM_OPTIONS.indexOf(svc.memory) : -1;
	const [cpuIndex, setCpuIndex] = useState(initCpuIdx >= 0 ? initCpuIdx : 4);
	const [memoryIndex, setMemoryIndex] = useState(
		initMemIdx >= 0 ? initMemIdx : 1,
	);
	const [replicas, setReplicas] = useState(activeDep?.replicas ?? 1);

	const [specSeed, setSpecSeed] = useState({ open, activeDep });
	if (specSeed.open !== open || specSeed.activeDep !== activeDep) {
		setSpecSeed({ open, activeDep });
		if (open) {
			const s = activeDep ? getServiceSpec(activeDep) : undefined;
			const ci = s?.cpu ? CPU_OPTIONS.indexOf(s.cpu) : -1;
			const mi = s?.memory ? MEM_OPTIONS.indexOf(s.memory) : -1;
			setCpuIndex(ci >= 0 ? ci : 4);
			setMemoryIndex(mi >= 0 ? mi : 1);
			setReplicas(activeDep?.replicas ?? 1);
		}
	}

	// Name state
	const [name, setName] = useState(initialName);
	const [nameSeed, setNameSeed] = useState({ open, initialName });
	if (nameSeed.open !== open || nameSeed.initialName !== initialName) {
		setNameSeed({ open, initialName });
		if (open) setName(initialName);
	}

	// Domain state — editable subdomain
	const primaryDomain = domains[0];
	const domainStr = primaryDomain?.domain ?? "";
	const dotIdx = domainStr.indexOf(".");
	const initSubdomain = dotIdx > -1 ? domainStr.slice(0, dotIdx) : domainStr;
	const domainSuffix = dotIdx > -1 ? domainStr.slice(dotIdx) : "";
	const [subdomain, setSubdomain] = useState(initSubdomain);
	const [domainSeed, setDomainSeed] = useState({ open, domains });
	if (domainSeed.open !== open || domainSeed.domains !== domains) {
		setDomainSeed({ open, domains });
		if (open) {
			const d = domains[0]?.domain ?? "";
			const di = d.indexOf(".");
			setSubdomain(di > -1 ? d.slice(0, di) : d);
		}
	}

	// Danger zone
	const [confirmDelete, setConfirmDelete] = useState(false);

	const { mutate: scale, isPending: scaling } = useMutation(scaleResource);
	const { mutate: update, isPending: updating } = useMutation(updateResource);
	const { mutate: del, isPending: deleting } = useMutation(deleteResource);
	const { mutate: updateDomain, isPending: savingDomain } =
		useMutation(updateResourceDomain);

	const handleScale = () => {
		scale(
			{
				resourceId,
				replicas,
				cpu: CPU_OPTIONS[cpuIndex] ?? DEFAULT_CPU,
				memory: MEM_OPTIONS[memoryIndex] ?? DEFAULT_MEM,
			},
			{
				onSuccess: () => {
					toast.success("Scaling applied");
				},
				onError: () => {
					toast.error("Failed to scale");
				},
			},
		);
	};

	const handleNameSave = () => {
		if (!name.trim() || name.trim() === initialName) return;
		update(
			{ resourceId, name: name.trim() },
			{
				onSuccess: () => {
					toast.success("Resource renamed");
				},
				onError: () => {
					toast.error("Failed to rename");
				},
			},
		);
	};

	const handleDomainSave = () => {
		if (
			!primaryDomain?.id ||
			!subdomain.trim() ||
			subdomain.trim() === initSubdomain
		)
			return;
		const newDomain = `${subdomain.trim()}${domainSuffix}`;
		updateDomain(
			{ domainId: primaryDomain.id, domain: newDomain },
			{
				onSuccess: () => {
					toast.success("Domain updated");
				},
				onError: () => {
					toast.error("Failed to update domain");
				},
			},
		);
	};

	const handleDelete = () => {
		del(
			{ resourceId },
			{
				onSuccess: () => {
					toast.success("Resource deleted");
					onClose();
					if (activeOrgId && activeWorkspaceId)
						void navigate(`/org/${activeOrgId}/wks/${activeWorkspaceId}`);
				},
				onError: () => {
					toast.error("Failed to delete resource");
				},
			},
		);
	};

	const sec = (title: string) => (
		<div className="text-[10px] font-bold text-[#b0a090] tracking-[0.08em] uppercase mb-3">
			{title}
		</div>
	);
	const divider = <div className="border-t border-[#ede7dd] my-6" />;

	return (
		<>
			<div
				className={cn(
					"fixed inset-0 bg-[rgba(42,32,24,0.12)] z-40 transition-opacity duration-280 ease-in",
					open
						? "opacity-100 pointer-events-auto"
						: "opacity-0 pointer-events-none",
				)}
				onClick={onClose}
			/>
			<div
				className={cn(
					"fixed top-0 right-0 bottom-0 w-[min(440px,100vw)] bg-card border-l border-[#e8e0d4] z-50 flex flex-col shadow-[-8px_0_40px_rgba(42,32,24,0.12)] transition-transform duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
					open ? "translate-x-0" : "translate-x-full",
				)}
			>
				{/* header */}
				<div className="px-[22px] py-4 border-b border-[#e8e0d4] flex items-center justify-between">
					<span className="font-serif text-[18px]">Settings</span>
					<button
						onClick={onClose}
						className="bg-transparent border-none cursor-pointer text-[#8a7a68] p-1 rounded-sm"
					>
						<svg
							width="17"
							height="17"
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

				{/* body */}
				<div className="flex-1 overflow-y-auto px-[22px] py-6">
					{/* Scale */}
					{sec("Scale")}
					<div className="flex flex-col gap-5">
						<div>
							<div className="flex items-center justify-between mb-2">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									Replicas
								</span>
								<span className="text-[13px] font-semibold text-foreground">
									{replicas}
								</span>
							</div>
							<Input
								type="number"
								min="1"
								max="20"
								value={replicas}
								onChange={(e) => {
									const n = parseInt(e.target.value, 10);
									if (!isNaN(n) && n > 0) setReplicas(n);
								}}
								className="w-24 text-center font-mono text-[13px]"
							/>
						</div>
						<div>
							<div className="flex items-center justify-between mb-2.5">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									CPU
								</span>
								<span className="font-mono text-[12px] font-semibold text-foreground bg-[#ede7dd] px-2 py-0.5 rounded-[5px]">
									{CPU_OPTIONS[cpuIndex]}
								</span>
							</div>
							<Slider
								value={[cpuIndex]}
								onValueChange={(v) => {
									setCpuIndex(v[0] ?? 0);
								}}
								min={0}
								max={CPU_OPTIONS.length - 1}
								step={1}
								className="w-full"
							/>
							<div className="flex justify-between mt-1.5">
								<span className="text-[10px] text-[#b0a090]">100m</span>
								<span className="text-[10px] text-[#b0a090]">2000m</span>
							</div>
						</div>
						<div>
							<div className="flex items-center justify-between mb-2.5">
								<span className="text-[13px] text-[#4a3c30] font-medium">
									Memory
								</span>
								<span className="font-mono text-[12px] font-semibold text-foreground bg-[#ede7dd] px-2 py-0.5 rounded-[5px]">
									{MEM_OPTIONS[memoryIndex]}
								</span>
							</div>
							<Slider
								value={[memoryIndex]}
								onValueChange={(v) => {
									setMemoryIndex(v[0] ?? 0);
								}}
								min={0}
								max={MEM_OPTIONS.length - 1}
								step={1}
								className="w-full"
							/>
							<div className="flex justify-between mt-1.5">
								<span className="text-[10px] text-[#b0a090]">256Mi</span>
								<span className="text-[10px] text-[#b0a090]">2Gi</span>
							</div>
						</div>
						<button
							onClick={handleScale}
							disabled={scaling}
							className={cn(
								"self-start inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-[#2a2018] text-[#f7f3ec] border-none text-[13px] font-medium font-sans",
								scaling
									? "opacity-60 cursor-not-allowed"
									: "cursor-pointer hover:bg-[#3d2f20] transition-colors",
							)}
						>
							{scaling && (
								<svg
									className="animate-spin"
									width="12"
									height="12"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									strokeWidth="2"
								>
									<path d="M21 12a9 9 0 1 1-6.219-8.56" />
								</svg>
							)}
							{scaling ? "Applying…" : "Apply"}
						</button>
					</div>

					{divider}

					{/* Domain */}
					{sec("Domain")}
					{primaryDomain ? (
						<div className="flex flex-col gap-2">
							<div className="flex gap-1.5 items-center">
								<Input
									value={subdomain}
									onChange={(e) => {
										setSubdomain(
											e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""),
										);
									}}
									placeholder="subdomain"
									className="font-mono text-[12px] flex-1"
								/>
								{domainSuffix && (
									<span className="font-mono text-[12px] text-[#a0907e] bg-[#f0ebe3] px-2.5 py-2 rounded-[7px] border border-[#e0d8cc] whitespace-nowrap shrink-0">
										{domainSuffix}
									</span>
								)}
							</div>
							<div className="flex gap-2 items-center">
								<a
									href={`https://${domainStr}`}
									target="_blank"
									rel="noopener noreferrer"
									className="font-mono text-[11px] text-[#4a6b9c]! no-underline flex-1 overflow-hidden text-ellipsis whitespace-nowrap"
								>
									↗ {subdomain || "…"}
									{domainSuffix}
								</a>
								<button
									onClick={handleDomainSave}
									disabled={
										savingDomain ||
										!subdomain.trim() ||
										subdomain.trim() === initSubdomain
									}
									className={cn(
										"inline-flex items-center gap-[5px] px-3 py-1.5 rounded-[7px] bg-transparent text-[#4a3c30] border border-[#ddd5c8] cursor-pointer text-xs font-sans whitespace-nowrap",
										savingDomain ||
											!subdomain.trim() ||
											subdomain.trim() === initSubdomain
											? "opacity-50"
											: "",
									)}
								>
									{savingDomain ? "Saving…" : "Save"}
								</button>
							</div>
						</div>
					) : (
						<div className="text-[13px] text-[#a0907e]">
							No domain configured
						</div>
					)}

					{divider}

					{/* General */}
					{sec("General")}
					<div className="flex gap-2 items-center">
						<Input
							value={name}
							onChange={(e) => {
								setName(e.target.value);
							}}
							placeholder="Resource name"
							className="font-sans text-[13px] flex-1"
						/>
						<button
							onClick={handleNameSave}
							disabled={updating || !name.trim() || name.trim() === initialName}
							className={cn(
								"inline-flex items-center gap-[5px] px-3.5 py-2 rounded-lg bg-transparent text-[#4a3c30] border border-[#ddd5c8] cursor-pointer text-[13px] font-medium font-sans whitespace-nowrap",
								updating || !name.trim() || name.trim() === initialName
									? "opacity-50"
									: "",
							)}
						>
							{updating ? "Saving…" : "Save"}
						</button>
					</div>

					{divider}

					{/* Danger zone */}
					{sec("Danger Zone")}
					{!confirmDelete ? (
						<button
							onClick={() => {
								setConfirmDelete(true);
							}}
							className="inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg bg-[#fdeaea] text-[#8b2e2e] border border-[#f0c8c8] cursor-pointer text-[13px] font-medium font-sans"
						>
							<svg
								width="13"
								height="13"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<polyline points="3 6 5 6 21 6" />
								<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
								<path d="M10 11v6M14 11v6M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
							</svg>
							Delete resource
						</button>
					) : (
						<div className="p-3.5 bg-[#fdeaea] border border-[#f0c8c8] rounded-md">
							<p className="text-[13px] text-[#8b2e2e] mb-3 font-medium">
								Are you sure? This cannot be undone.
							</p>
							<div className="flex gap-2">
								<button
									onClick={handleDelete}
									disabled={deleting}
									className={cn(
										"inline-flex items-center gap-[5px] px-3.5 py-[7px] rounded-[7px] bg-[#8b2e2e] text-white border-none text-xs font-semibold font-sans",
										deleting
											? "opacity-70 cursor-not-allowed"
											: "cursor-pointer",
									)}
								>
									{deleting ? "Deleting…" : "Yes, delete"}
								</button>
								<button
									onClick={() => {
										setConfirmDelete(false);
									}}
									className="px-3 py-[7px] rounded-[7px] bg-transparent text-[#6b5d4f] border border-[#ddd5c8] cursor-pointer text-xs font-sans"
								>
									Cancel
								</button>
							</div>
						</div>
					)}
				</div>
			</div>
		</>
	);
}

// ─── Main Page ────────────────────────────────────────────────────────────────
