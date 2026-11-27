import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { AllocationCoverageSchema } from "@gen/loco/capacity/v1/capacity_pb";
import { reservationLabel } from "./reservation";

test("missing cluster coverage never turns an unobserved reservation into zero", () => {
	const complete = create(AllocationCoverageSchema, { reportingClusters: 1 });
	const stale = create(AllocationCoverageSchema, { reportingClusters: 1, staleClusters: 1 });
	const partial = create(AllocationCoverageSchema, { reportingClusters: 1, missingClusters: 1 });
	expect(reservationLabel(0n, false, undefined)).toBe("Not reported");
	expect(reservationLabel(0n, false, partial)).toBe("Not reported");
	expect(reservationLabel(0n, false, stale)).toBe("Not reported");
	expect(reservationLabel(500n, false, stale)).toBe("0.5 vCPU");
	expect(reservationLabel(0n, false, complete)).toBe("0 vCPU");
	expect(reservationLabel(500n, false, partial)).toBe("0.5 vCPU");
});
