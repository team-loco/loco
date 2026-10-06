import {
	CheckIcon,
	ChevronDownIcon,
	ChevronUpIcon,
	CircleCheckIcon,
	CircleXIcon,
	GaugeIcon,
	GlobeIcon,
	LoaderCircleIcon,
	LockIcon,
	MapPinIcon,
} from "lucide-react";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/design/Collapsible";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { SheetSection } from "@/components/design/Sheet";
import { SoonTag } from "@/components/design/SoonTag";
import { Stepper } from "@/components/design/Stepper";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

import { EnvVarsEditor, StopSlider } from "./DraftFields";
import { CPU_STOPS, MAX_REPLICAS, MEMORY_STOPS, type Draft } from "./drafts";
import type { DraftErrors } from "./draftValidation";

function regionNote(r: RegionInfo): { label: string; tone: "muted" | "warn" } | null {
	const health = r.healthStatus.toLowerCase();
	if (health !== "" && health !== "healthy" && health !== "ok") return { label: health, tone: "warn" };
	if (r.isDefault) return { label: "default", tone: "muted" };
	return null;
}

function NoteBadge({ note }: { note: { label: string; tone: "muted" | "warn" } | null }) {
	if (note === null) return null;
	return (
		<Badge size="sm" tone={note.tone} className="h-auto px-1.5">
			{note.label}
		</Badge>
	);
}

export function DraftForm({
	draft,
	region,
	regions,
	platformDomain,
	errors,
	tried,
	availability,
	healthOpen,
	onHealthOpenChange,
	update,
}: {
	draft: Draft;
	region: string;
	regions: RegionInfo[];
	platformDomain: string;
	errors: DraftErrors;
	tried: boolean;
	availability: "available" | "taken" | "unknown" | "checking";
	healthOpen: boolean;
	onHealthOpenChange: (open: boolean) => void;
	update: (patch: Partial<Draft>) => void;
}) {
	const current = regions.find((r) => r.region === region);
	const netError = errors.sub ?? errors.port;
	const sub = draft.sub.trim();
	const showPortError = errors.port !== null && (tried || draft.port !== "");

	const setMin = (min: number) => {
		update({ min, max: Math.max(min, draft.max) });
	};
	const setMax = (max: number) => {
		update({ max, min: Math.min(max, draft.min) });
	};

	return (
		<>
			<SheetSection title="Region" className="gap-2.5">
				<DropdownMenu>
					<DropdownMenuTrigger
						render={<Button variant="outline" className="h-9 w-full justify-start gap-2 px-2.5" />}
					>
						<MapPinIcon className="text-fg3" />
						<span className="font-medium">{region === "" ? "No regions available" : region}</span>
						{current !== undefined && <NoteBadge note={regionNote(current)} />}
						<span className="flex-1" />
						<ChevronDownIcon className="text-fg3" />
					</DropdownMenuTrigger>
					<DropdownMenuContent>
						{regions.map((r) => (
							<DropdownMenuItem
								key={r.region}
								className={cn("h-[34px] gap-2", r.region === region && "bg-bg3")}
								onClick={() => {
									update({ region: r.region });
								}}
							>
								<span className="flex-1">{r.region}</span>
								<NoteBadge note={regionNote(r)} />
								<span className="flex w-3.5">{r.region === region && <CheckIcon className="size-3.5" />}</span>
							</DropdownMenuItem>
						))}
					</DropdownMenuContent>
				</DropdownMenu>
				{tried && errors.region !== null && <span className="text-sm text-bad-fg">{errors.region}</span>}
			</SheetSection>

			<SheetSection title="Networking">
				<ToggleGroup variant="segmented" value={["public"]} className="grid w-full grid-cols-2">
					<ToggleGroupItem
						value="private"
						disabled
						title="Private services are not available yet: every service needs a public domain to deploy"
						className="cursor-not-allowed gap-1.5 text-fg4 data-disabled:pointer-events-auto data-disabled:opacity-100"
					>
						<LockIcon className="size-3.5" />
						Private
						<SoonTag />
					</ToggleGroupItem>
					<ToggleGroupItem value="public" className="gap-1.5">
						<GlobeIcon className="size-3.5" />
						Public
					</ToggleGroupItem>
				</ToggleGroup>
				<div className="grid grid-cols-[minmax(0,1fr)_96px] gap-2">
					<div className="flex min-w-0 flex-col gap-1.5">
						<span className="text-sm text-fg3">URL</span>
						<InputGroup className="h-[34px] overflow-hidden has-[>[data-align=inline-end]]:[&>input]:pr-0.5 has-[>[data-align=inline-start]]:[&>input]:pl-0.5">
							<InputGroupAddon>
								<InputGroupText>https://</InputGroupText>
							</InputGroupAddon>
							<InputGroupInput
								value={draft.sub}
								aria-label="Subdomain"
								aria-invalid={errors.sub !== null && sub !== ""}
								onChange={(e) => {
									update({ sub: e.target.value.toLowerCase() });
								}}
							/>
							<InputGroupAddon align="inline-end" className="h-full border-l border-line bg-bg2 px-2.5 py-0 text-fg2">
								.{platformDomain}
							</InputGroupAddon>
						</InputGroup>
					</div>
					<Field label="Port">
						<Input
							value={draft.port}
							placeholder="8000"
							title="Port between 1024 and 65535"
							inputMode="numeric"
							aria-invalid={showPortError}
							className="h-[34px]"
							onChange={(e) => {
								update({ port: e.target.value });
							}}
						/>
					</Field>
				</div>
				<span
					className={cn(
						"flex items-center gap-1.5 text-sm",
						netError !== null ? "text-bad-fg" : availability === "available" ? "text-ok-fg" : "text-fg3",
					)}
				>
					{netError !== null ? (
						<CircleXIcon className="size-[13px]" />
					) : availability === "available" ? (
						<CircleCheckIcon className="size-[13px]" />
					) : availability === "checking" ? (
						<LoaderCircleIcon className="size-[13px] animate-spin" />
					) : null}
					{netError ??
						(availability === "available"
							? `${sub}.${platformDomain} is available`
							: availability === "checking"
								? `Checking ${sub}.${platformDomain}…`
								: `Availability of ${sub}.${platformDomain} is confirmed on deploy`)}
				</span>
			</SheetSection>

			<SheetSection title="Scaling">
				<div className="flex gap-4">
					<div className="flex flex-col gap-1.5">
						<span className="text-sm text-fg3">Min replicas</span>
						<Stepper label="Min replicas" value={draft.min} min={1} max={MAX_REPLICAS} onChange={setMin} />
					</div>
					<div className="flex flex-col gap-1.5">
						<span className="text-sm text-fg3">Max replicas</span>
						<Stepper label="Max replicas" value={draft.max} min={1} max={MAX_REPLICAS} onChange={setMax} />
					</div>
				</div>
				{draft.min !== draft.max && (
					<div className="flex flex-col gap-1.5">
						<div className="flex items-center gap-2 rounded-sm border border-line bg-bg2 px-3 py-2.5">
							<GaugeIcon className="size-3.5 text-fg3" />
							<span>Scale on CPU above</span>
							<Input
								value={draft.cpuTarget}
								inputMode="numeric"
								aria-label="CPU target"
								aria-invalid={errors.cpuTarget !== null}
								className="h-7 w-[52px] px-2 text-right"
								onChange={(e) => {
									update({ cpuTarget: e.target.value });
								}}
							/>
							<span className="text-fg3">%</span>
						</div>
						{errors.cpuTarget !== null && <span className="text-sm text-bad-fg">{errors.cpuTarget}</span>}
					</div>
				)}
			</SheetSection>

			<SheetSection title="Size per replica" className="gap-4">
				<StopSlider
					label="CPU"
					stops={CPU_STOPS}
					value={draft.cpu}
					format={(v) => (v.endsWith("m") ? v : `${v} CPU`)}
					onChange={(cpu) => {
						update({ cpu });
					}}
				/>
				<StopSlider
					label="Memory"
					stops={MEMORY_STOPS}
					value={draft.memory}
					format={(v) => v}
					onChange={(memory) => {
						update({ memory });
					}}
				/>
			</SheetSection>

			<SheetSection title="Environment variables" className="gap-2.5">
				<EnvVarsEditor
					vars={draft.vars}
					onChange={(vars) => {
						update({ vars });
					}}
				/>
				{errors.vars !== null && <span className="text-sm text-bad-fg">{errors.vars}</span>}
			</SheetSection>

			<Collapsible open={healthOpen} onOpenChange={onHealthOpenChange}>
				<CollapsibleTrigger className="flex w-full items-center gap-2.5 px-6 py-[18px] text-left text-foreground hover:bg-bg2">
					<span className="font-semibold">Health check</span>
					<span className={cn("flex-1 text-sm", errors.health !== null ? "text-bad-fg" : "text-fg3")}>
						{errors.health ?? `${draft.hcPath} · every ${draft.hcInterval}s`}
					</span>
					{healthOpen ? (
						<ChevronUpIcon className="size-3.5 text-fg3" />
					) : (
						<ChevronDownIcon className="size-3.5 text-fg3" />
					)}
				</CollapsibleTrigger>
				<CollapsibleContent>
					<div className="grid grid-cols-[minmax(0,1.4fr)_repeat(3,minmax(0,1fr))] gap-2 px-6 pb-5">
						<Field label="Path">
							<Input
								value={draft.hcPath}
								aria-invalid={!draft.hcPath.startsWith("/")}
								onChange={(e) => {
									update({ hcPath: e.target.value });
								}}
							/>
						</Field>
						<Field label="Interval">
							<Input
								value={draft.hcInterval}
								inputMode="numeric"
								onChange={(e) => {
									update({ hcInterval: e.target.value });
								}}
							/>
						</Field>
						<Field label="Timeout">
							<Input
								value={draft.hcTimeout}
								inputMode="numeric"
								onChange={(e) => {
									update({ hcTimeout: e.target.value });
								}}
							/>
						</Field>
						<Field label="Fail after">
							<Input
								value={draft.hcFail}
								inputMode="numeric"
								onChange={(e) => {
									update({ hcFail: e.target.value });
								}}
							/>
						</Field>
					</div>
				</CollapsibleContent>
			</Collapsible>
		</>
	);
}
