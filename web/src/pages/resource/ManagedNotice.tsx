import { Notice } from "@/components/design/Notice";

export const MANAGED_NOTICE_ID = "managed-notice";

export function ManagedNotice({ partial }: { partial: string }) {
	return (
		<Notice id={MANAGED_NOTICE_ID}>
			Managed by loco.yaml (<span className="break-all">{partial}</span>), so these settings are read-only here. Change them in the file and run{" "}
			<code>loco infra apply</code>.
		</Notice>
	);
}
