import { BoxIcon, Building2Icon, GlobeIcon, LayersIcon, UserIcon } from "lucide-react";
import type { ReactNode } from "react";

import type { NodeKind, OwnerKey } from "./model";

export function kindIcon(kind: NodeKind): ReactNode {
	switch (kind) {
		case "org":
			return <Building2Icon />;
		case "ws":
			return <LayersIcon />;
		case "env":
			return <LayersIcon />;
		case "res":
			return <BoxIcon />;
		case "user":
			return <UserIcon />;
		case "system":
			return <GlobeIcon />;
	}
}

export function ownerIcon(owner: OwnerKey): ReactNode {
	switch (owner) {
		case "org":
			return <Building2Icon />;
		case "workspace":
			return <LayersIcon />;
		case "personal":
			return <UserIcon />;
	}
}
