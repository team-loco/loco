import type { AllocationConsumer, GetNodeAllocationsResponse } from "@gen/loco/cluster/v1/cluster_pb";

export interface AllocationTile {
	id: string;
	name: string;
	value: number;
	consumer?: AllocationConsumer;
	available?: boolean;
}

export function allocationValue(consumer: AllocationConsumer, memory: boolean): bigint {
	return memory ? consumer.memoryBytes : consumer.cpuMillicores;
}

export function allocationTiles(response: GetNodeAllocationsResponse): AllocationTile[] {
	const consumers = [...response.top];
	if (response.remainder !== undefined) consumers.push(response.remainder);
	if (response.system !== undefined) consumers.push(response.system);
	const tiles: AllocationTile[] = consumers.map((consumer) => ({
		id: `${consumer.kind}:${consumer.id}`,
		name: consumer.name,
		value: Number(allocationValue(consumer, response.memory)),
		consumer,
	}));
	const node = response.node;
	if (node !== undefined && response.workspaceId === "") {
		const allocatable = response.memory ? node.memoryBytesAllocatable : node.cpuMillicoresAllocatable;
		const requested = response.memory ? node.memoryBytesRequested : node.cpuMillicoresRequested;
		const reservationsCurrent = response.sample !== undefined && !response.sample.stale;
		const current = reservationsCurrent && !node.capacityStale;
		const headroomKnown = current && allocatable !== undefined;
		if (headroomKnown && allocatable > requested) {
			tiles.push({
				id: "capacity:available",
				name: "Available",
				value: Number(allocatable - requested),
				available: true,
			});
		}
	}
	return tiles.filter((tile) => tile.value > 0);
}

// The bounded server summary keeps every rectangle proportional to its reservation.
export function allocationRects<T extends AllocationTile>(
	tiles: T[],
	x = 0,
	y = 0,
	width = 100,
	height = 100,
): (T & { x: number; y: number; width: number; height: number })[] {
	if (tiles.length === 0) return [];
	if (tiles.length === 1) return [{ ...tiles[0]!, x, y, width, height }];
	const total = tiles.reduce((sum, tile) => sum + tile.value, 0);
	let split = 1;
	let subtotal = tiles[0]!.value;
	while (split < tiles.length - 1 && subtotal < total / 2) {
		const next = tiles[split]!.value;
		if (Math.abs(subtotal - total / 2) < Math.abs(subtotal + next - total / 2)) break;
		subtotal += next;
		split++;
	}
	const share = subtotal / total;
	if (width * 1.8 >= height)
		return [
			...allocationRects(tiles.slice(0, split), x, y, width * share, height),
			...allocationRects(tiles.slice(split), x + width * share, y, width * (1 - share), height),
		];
	return [
		...allocationRects(tiles.slice(0, split), x, y, width, height * share),
		...allocationRects(tiles.slice(split), x, y + height * share, width, height * (1 - share)),
	];
}
