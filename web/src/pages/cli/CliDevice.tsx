import { Spinner } from "@/components/design/Spinner";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState, type SubmitEvent } from "react";
import { useQueryState } from "nuqs";

import { approveDeviceLogin, getDeviceLogin } from "@gen/loco/auth/v1/auth-AuthService_connectquery";

import { Button } from "@/components/design/Button";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { getErrorMessage } from "@/lib/error-handler";
import { formatHourMinute, formatMonthDayYear, maybeTsMs } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { describeDevice } from "@/pages/settings/sessions";

import { CliCard } from "./CliCard";
import { TerminalCode } from "./TerminalCode";

const USER_CODE_LENGTH = 8;

function DeviceDetails({ userCode }: { userCode: string }) {
	const pref = useTimePreferences();
	const code = userCode.replace(/[^a-z0-9]/gi, "");
	const device = useQuery(getDeviceLogin, { userCode }, { enabled: code.length === USER_CODE_LENGTH, retry: false });
	if (device.data === undefined) return null;
	const { label, detail } = describeDevice(device.data.userAgent, device.data.ipAddress);
	const requested = maybeTsMs(device.data.requestedAt);
	const rows = [
		["Device", label],
		["System", detail],
		["IP address", device.data.ipAddress],
		[
			"Requested",
			requested === undefined ? "" : `${formatMonthDayYear(requested, pref)}, ${formatHourMinute(requested, pref)}`,
		],
	].filter(([, value]) => value !== "");
	return (
		<dl
			aria-label="Requesting device"
			className="m-0 flex flex-col gap-1.5 rounded-[10px] bg-panel px-4 py-3 text-[12.5px]"
		>
			{rows.map(([term, value]) => (
				<div key={term} className="flex justify-between gap-3">
					<dt className="text-fg3">{term}</dt>
					<dd className="m-0 min-w-0 text-right font-mono break-words">{value}</dd>
				</div>
			))}
		</dl>
	);
}

export function CliDevice() {
	const [linkedCode] = useQueryState("code");
	const [userCode, setUserCode] = useState(linkedCode ?? "");
	const approve = useMutation(approveDeviceLogin);

	const submit = (event: SubmitEvent<HTMLFormElement>) => {
		event.preventDefault();
		approve.mutate({ userCode });
	};

	const errorText = approve.error !== null ? getErrorMessage(approve.error, "Could not sign in the device") : undefined;

	if (approve.isSuccess) {
		return (
			<CliCard title="Device signed in">
				{() => <p className="m-0 text-center text-[13.5px] text-fg2">You can return to your terminal.</p>}
			</CliCard>
		);
	}

	return (
		<CliCard title="Sign in a device" terminalCode={linkedCode}>
			{(email) => (
				<form className="flex flex-col gap-4" onSubmit={submit}>
					<p className="m-0 text-center text-[13.5px] text-fg2">
						{linkedCode === null ? "Enter the code shown by " : "Approve the device running "}
						<code className="font-mono text-[0.92em] whitespace-nowrap text-foreground">loco login --device</code> to
						sign it in as <span className="font-medium text-foreground">{email}</span>.
					</p>
					{linkedCode === null ? (
						<Field label="Device code" error={errorText}>
							<Input
								value={userCode}
								required
								autoComplete="off"
								autoCapitalize="characters"
								spellCheck={false}
								placeholder="XXXX-XXXX"
								className="font-mono tracking-widest uppercase"
								onChange={(event) => {
									setUserCode(event.target.value);
									if (approve.isError) approve.reset();
								}}
							/>
						</Field>
					) : (
						<TerminalCode code={linkedCode}>
							{errorText !== undefined && (
								<span role="alert" className="text-[12.5px] text-bad-fg">
									{errorText}
								</span>
							)}
						</TerminalCode>
					)}
					<DeviceDetails userCode={userCode} />
					<Button type="submit" size="xl" className="w-full" disabled={approve.isPending || userCode.trim() === ""}>
						{approve.isPending && <Spinner className="size-4" />}
						Approve
					</Button>
				</form>
			)}
		</CliCard>
	);
}
