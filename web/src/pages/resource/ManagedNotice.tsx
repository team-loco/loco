import type { ReactNode } from "react";

import { Notice } from "@/components/design/Notice";

export const MANAGED_NOTICE_ID = "managed-notice";

export function ManagedNotice({ partial, children }: { partial: string; children?: ReactNode }) {
	return (
		<Notice id={MANAGED_NOTICE_ID}>
			{children ?? (
				<>
					Managed by loco.yaml (<span className="break-all">{partial}</span>), so these settings are read-only here.
					Change them in the file and run <code>loco infra apply</code>.
				</>
			)}
		</Notice>
	);
}
