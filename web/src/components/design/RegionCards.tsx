import { RegionAvailability, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";
import type { ComponentProps, ReactNode } from "react";

import { cn } from "@/lib/utils";
import { ToggleGroup, ToggleGroupItem } from "./ToggleGroup";
import { RegionHealth } from "./RegionHealth";
import "./RegionCards.css";

export function RegionCards({ className, ...props }: ComponentProps<typeof ToggleGroup>) {
	return <ToggleGroup multiple variant="outline" className={cn("reg-cards w-full", className)} {...props} />;
}

export function RegionCard({
	name,
	health,
	configured = false,
	note,
	noteTone,
	className,
	disabled,
	...props
}: Omit<ComponentProps<typeof ToggleGroupItem>, "children"> & {
	name: string;
	health: RegionInfo | undefined;
	configured?: boolean;
	note?: ReactNode;
	noteTone?: "add" | "rm" | "warn" | undefined;
}) {
	const unavailable =
		health === undefined
			? "No cluster in this region"
			: health.availability === RegionAvailability.OFFLINE
				? "Offline"
				: undefined;
	const shownNote = noteTone === "rm" ? note : (unavailable ?? note);
	return (
		<ToggleGroupItem
			className={cn("reg-card", className)}
			{...props}
			disabled={disabled === true || (!configured && unavailable !== undefined)}
		>
			<span className="flex w-full flex-wrap items-center gap-2">
				<span className="font-mono text-[12.5px]">{name}</span>
				{unavailable === undefined && <RegionHealth region={health} />}
			</span>
			{shownNote !== undefined && (
				<span className="reg-note" data-tone={unavailable === undefined || noteTone === "rm" ? noteTone : "warn"}>
					{shownNote}
				</span>
			)}
		</ToggleGroupItem>
	);
}
