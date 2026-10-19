import { Notice } from "@/components/design/Notice";

export const MANAGED_NOTICE_ID = "managed-notice";

export function ManagedNotice() {
	return (
		<Notice id={MANAGED_NOTICE_ID}>
			Managed by loco.yaml. Change settings in the file and run <code>loco infra apply</code>.
		</Notice>
	);
}
