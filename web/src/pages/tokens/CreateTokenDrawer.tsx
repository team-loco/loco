import { useTimePreferences } from "@/lib/use-time-preferences";
import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { CheckIcon, EyeIcon, EyeOffIcon } from "lucide-react";
import { useRef, useState, type RefObject, type ReactNode } from "react";
import { createToken } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { TokenService, type Token } from "@gen/loco/token/v1/token_pb";

import { BASE_URL } from "@/auth/connect-transport";
import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { DateTimePicker } from "@/components/design/DateTimePicker";
import { Field } from "@/components/design/Field";
import { InlineCode } from "@/components/design/InlineCode";
import { Input } from "@/components/design/Input";
import { Notice } from "@/components/design/Notice";
import { SecretValue } from "@/components/design/SecretValue";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { pluralize } from "@/lib/format";
import { toLocalInput, fromLocalInput } from "@/lib/obs-time";
import { cn } from "@/lib/utils";
import { DAY_MS, formatMonthDayYear, formatHourMinute, maybeTsMs } from "@/lib/time";

import { TokenScopeList } from "./TokenScopes";
import { AccessPicker } from "./AccessPicker";
import type { AccessItem } from "./AccessPicker";
import {
	allowsFor,
	customExpiryError,
	defaultExpirySeconds,
	expiryOptions,
	grantsToScopes,
	OWNER_KEYS,
	OWNER_TITLE,
	sanitizeTokenName,
	TOKEN_NAME_RE,
} from "./model";
import type { OwnerKey } from "./model";
import type { TokenData } from "./useTokenData";

export interface CreateDraft {
	owner: OwnerKey;
	name: string;
	items: AccessItem[];
}

const NAME_MAX = 100;
const MS_PER_SECOND = 1000;
const CUSTOM = "custom";
const CUSTOM_DEFAULT_MS = 3 * DAY_MS;

function apiExample(token: string): string {
	return [
		`curl -H "Authorization: Bearer ${token}" \\`,
		"  -H 'Content-Type: application/json' -d '{}' \\",
		`  ${BASE_URL}/${TokenService.typeName}/${TokenService.method.getScopes.name}`,
	].join("\n");
}

export function CreateTokenDrawer({
	draft,
	viewing = null,
	data,
	now,
	onClose,
	onCreated,
}: {
	draft: CreateDraft | null;
	viewing?: { token: Token; owner: OwnerKey } | null;
	data: TokenData;
	now: number;
	onClose: () => void;
	onCreated: (owner: OwnerKey) => void;
}) {
	const [secret, setSecret] = useState<{ name: string; value: string } | null>(null);
	const nameRef = useRef<HTMLInputElement>(null);
	const config = useQuery(getConfig, {}, { enabled: draft !== null });
	return (
		<Sheet
			open={draft !== null || viewing !== null}
			disablePointerDismissal={secret !== null}
			onOpenChange={(open) => {
				if (open) return;
				setSecret(null);
				onClose();
			}}
		>
			<SheetContent size="wide" initialFocus={viewing === null ? nameRef : undefined}>
				<SheetHeader>
					<SheetTitle>
						{viewing !== null ? "Token details" : secret === null ? "New token" : "Token created"}
					</SheetTitle>
				</SheetHeader>
				{viewing !== null && (
					<ReadOnlyToken token={viewing.token} owner={viewing.owner} data={data} onClose={onClose} />
				)}
				{secret !== null && (
					<Reveal
						name={secret.name}
						secret={secret.value}
						onDone={() => {
							setSecret(null);
							onClose();
						}}
					/>
				)}
				{secret === null && draft !== null && config.data !== undefined && (
					<CreateForm
						nameRef={nameRef}
						maximum={config.data.apiTokenMaxLifetimeSeconds}
						draft={draft}
						data={data}
						now={now}
						onCancel={onClose}
						onCreated={(owner, name, value) => {
							setSecret({ name, value });
							onCreated(owner);
						}}
					/>
				)}
				{secret === null && draft !== null && config.isPending && <SheetBody>Loading token settings…</SheetBody>}
				{secret === null && draft !== null && config.isError && (
					<SheetBody>
						<p role="alert">{getErrorMessage(config.error, "Failed to load token settings")}</p>
						<Button
							onClick={() => {
								void config.refetch();
							}}
						>
							Retry
						</Button>
					</SheetBody>
				)}
			</SheetContent>
		</Sheet>
	);
}

function TokenFields({
	name,
	owner,
	expires,
	permissions,
}: {
	name: ReactNode;
	owner: ReactNode;
	expires: ReactNode;
	permissions: ReactNode;
}) {
	return (
		<SheetBody>
			{name}
			<div className="flex flex-col gap-1.5">
				<span className="text-[13px] font-medium">Owner</span>
				{owner}
			</div>
			<div className="flex flex-col gap-1.5">
				<span className="text-[13px] font-medium">Expires</span>
				{expires}
			</div>
			<div className="flex flex-col gap-1.5">
				<span className="text-[13px] font-medium">Permissions</span>
				{permissions}
			</div>
		</SheetBody>
	);
}

function ReadOnlyToken({
	token,
	owner,
	data,
	onClose,
}: {
	token: Token;
	owner: OwnerKey;
	data: TokenData;
	onClose: () => void;
}) {
	const timePref = useTimePreferences();
	const expires = maybeTsMs(token.expiresAt);
	return (
		<>
			<TokenFields
				name={
					<Field label="Name">
						<Input readOnly value={token.name} className="font-mono" />
					</Field>
				}
				owner={<Input readOnly aria-label="Owner" value={`${OWNER_TITLE[owner]}: ${data.owners[owner].owner.label}`} />}
				expires={
					<Input
						readOnly
						aria-label="Expires"
						value={
							expires === undefined
								? "Never"
								: `${formatMonthDayYear(expires, timePref)}, ${formatHourMinute(expires, timePref)}`
						}
					/>
				}
				permissions={<TokenScopeList token={token} entities={data.entities} />}
			/>
			<SheetFooter>
				<Button type="button" variant="outline" onClick={onClose}>
					Close
				</Button>
			</SheetFooter>
		</>
	);
}

function CreateForm({
	maximum,
	nameRef,
	draft,
	data,
	now,
	onCancel,
	onCreated,
}: {
	maximum: bigint;
	nameRef: RefObject<HTMLInputElement | null>;
	draft: CreateDraft;
	data: TokenData;
	now: number;
	onCancel: () => void;
	onCreated: (owner: OwnerKey, name: string, secret: string) => void;
}) {
	const timePref = useTimePreferences();
	const [ownerKey, setOwnerKey] = useState(draft.owner);
	const [typed, setTyped] = useState(draft.name);
	const [nameTouched, setNameTouched] = useState(false);
	const name = sanitizeTokenName(typed);
	const options = expiryOptions(maximum);
	const [selected, setSelected] = useState<number | typeof CUSTOM>(() => defaultExpirySeconds(maximum));
	const [custom, setCustom] = useState<{ ms: number; text: string | null }>(() => ({
		ms: fromLocalInput(toLocalInput(now + CUSTOM_DEFAULT_MS, timePref), timePref),
		text: null,
	}));
	const customMs = custom.ms;
	const customAt = custom.text ?? toLocalInput(customMs, timePref);
	const expiryErr = selected === CUSTOM ? customExpiryError(customMs, now, maximum) : null;
	const seconds =
		selected === CUSTOM ? Math.round((customMs - now) / MS_PER_SECOND) : Math.min(selected, Number(maximum));
	const [itemsByOwner, setItemsByOwner] = useState<Record<OwnerKey, AccessItem[]>>(() => ({
		org: [],
		ws: [],
		user: [],
		[draft.owner]: draft.items,
	}));
	const items = itemsByOwner[ownerKey];
	const setItems = (next: AccessItem[]) => {
		setItemsByOwner((prev) => ({ ...prev, [ownerKey]: next }));
	};
	const [nameTaken, setNameTaken] = useState<string | null>(null);
	const mutation = useMutation(createToken);
	const owner = data.owners[ownerKey].owner;

	const nameErr =
		nameTaken ??
		(name === ""
			? nameTouched
				? "Enter a name."
				: null
			: !TOKEN_NAME_RE.test(name)
				? "Use at least 2 characters: a–z, 0–9 and dashes."
				: data.owners[ownerKey].tokens.some((t) => t.name === name)
					? "A token with this name already exists."
					: null);
	const valid = name !== "" && nameErr === null && items.length > 0 && expiryErr === null;
	const allows = allowsFor(items.map((r) => ({ kind: r.node.kind, level: r.level })));
	const submit = async () => {
		if (name === "") setNameTouched(true);
		if (mutation.isPending || owner.id === null || !owner.canCreate || !valid) return;
		try {
			const res = await mutation.mutateAsync({
				name,
				entityType: owner.entityType,
				entityId: owner.id,
				scopes: grantsToScopes(items),
				expiresInSec: BigInt(seconds),
			});
			onCreated(ownerKey, name, res.token);
		} catch (err) {
			if (err instanceof ConnectError && err.code === Code.AlreadyExists) {
				setNameTaken(getErrorMessage(err));
				return;
			}
			toastConnectError(err, "Failed to create token");
		}
	};

	return (
		<form
			noValidate
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				void submit();
			}}
		>
			<TokenFields
				name={
					<Field
						label="Name"
						error={nameErr}
						reserve
						hint={
							name !== typed && name !== "" ? (
								<>
									Saved as <span className="font-mono">{name}</span>
								</>
							) : undefined
						}
					>
						<Input
							ref={nameRef}
							autoComplete="off"
							value={typed}
							maxLength={NAME_MAX}
							placeholder="github-actions-deploy"
							aria-invalid={nameErr !== null}
							className="font-mono"
							onChange={(e) => {
								setTyped(e.target.value);
								setNameTouched(true);
								setNameTaken(null);
							}}
						/>
					</Field>
				}
				owner={
					<ToggleGroup
						variant="segmented"
						aria-label="Owner"
						className="w-max"
						value={[ownerKey]}
						onValueChange={(v: string[]) => {
							const next = OWNER_KEYS.find((k) => k === v[0]);
							if (next === undefined || next === ownerKey) return;
							setOwnerKey(next);
							setNameTaken(null);
						}}
					>
						{OWNER_KEYS.map((k) => {
							const o = data.owners[k].owner;
							return (
								<ToggleGroupItem
									key={k}
									value={k}
									disabled={!o.canCreate}
									title={o.createHint ?? o.label}
									className="disabled:opacity-45"
								>
									{OWNER_TITLE[k]}
								</ToggleGroupItem>
							);
						})}
					</ToggleGroup>
				}
				expires={
					<>
						<ToggleGroup
							variant="segmented"
							aria-label="Expiry"
							className="w-max"
							value={[selected === CUSTOM ? CUSTOM : seconds.toString()]}
							onValueChange={(v: string[]) => {
								if (v[0] === CUSTOM) {
									setSelected(CUSTOM);
									return;
								}
								const next = options.find((d) => d.toString() === v[0]);
								if (next !== undefined) setSelected(next);
							}}
						>
							{options.map((d) => (
								<ToggleGroupItem key={d} value={d.toString()}>
									{(d * MS_PER_SECOND) % DAY_MS === 0
										? pluralize((d * MS_PER_SECOND) / DAY_MS, "day")
										: pluralize(d, "second")}
								</ToggleGroupItem>
							))}
							<ToggleGroupItem value={CUSTOM}>Custom</ToggleGroupItem>
						</ToggleGroup>
						{selected === CUSTOM && (
							<DateTimePicker
								label="Expires at"
								value={customAt}
								onChange={(value) => {
									const ms = fromLocalInput(value, timePref);
									setCustom({ ms, text: Number.isFinite(ms) ? null : value });
								}}
							/>
						)}
						{expiryErr === null ? (
							<span className="text-[12.5px] text-fg3">
								Expires {formatMonthDayYear(now + seconds * MS_PER_SECOND, timePref)},{" "}
								{formatHourMinute(now + seconds * MS_PER_SECOND, timePref)}
							</span>
						) : (
							<span className="text-[12.5px] text-bad-fg">{expiryErr}</span>
						)}
					</>
				}
				permissions={
					<>
						<AccessPicker
							key={ownerKey}
							roots={data.roots}
							org={data.activeOrg}
							held={data.held}
							items={items}
							allowUser={ownerKey === "user"}
							scope={ownerKey === "user" ? null : owner.node}
							onChange={setItems}
						/>
						<span className="text-[12.5px] text-fg3">You can only grant access you hold yourself.</span>
						{allows.length > 0 && (
							<ul className="m-0 mt-1.5 flex list-none flex-col gap-1.5 p-0">
								{allows.map((line) => (
									<li key={line} className="flex items-start gap-2 text-[12.5px] text-fg2">
										<CheckIcon className="mt-0.5 size-3.5 shrink-0 text-ok-fg" />
										{line}
									</li>
								))}
							</ul>
						)}
					</>
				}
			/>
			<SheetFooter>
				<Button type="button" variant="outline" onClick={onCancel}>
					Cancel
				</Button>
				<Button
					type="submit"
					disabled={mutation.isPending || !owner.canCreate || !valid}
					title={owner.createHint ?? undefined}
				>
					{mutation.isPending ? "Creating…" : "Create token"}
				</Button>
			</SheetFooter>
		</form>
	);
}

function Reveal({ name, secret, onDone }: { name: string; secret: string; onDone: () => void }) {
	const [shown, setShown] = useState(false);
	const [curlShown, setCurlShown] = useState(false);
	return (
		<>
			<SheetBody>
				<span className="flex items-center gap-2 text-[13.5px]">
					<StatusGlyph glyph="ok" />
					<span>
						<InlineCode>{name}</InlineCode> is ready.
					</span>
				</span>
				<SecretValue secret={secret} shown={shown} onShownChange={setShown} noun="token" />
				<Notice tone="warn">Copy it now. You won&apos;t be able to see it again.</Notice>
				<div className="flex flex-col gap-1.5">
					<div className="flex items-center justify-between gap-2">
						<span className="text-[13px] font-medium">Curl</span>
						<Button
							type="button"
							variant="ghost"
							size="icon-xs"
							aria-label={curlShown ? "Hide token in curl" : "Show token in curl"}
							title={curlShown ? "Hide token in curl" : "Show token in curl"}
							aria-pressed={curlShown}
							onClick={() => {
								setCurlShown(!curlShown);
							}}
						>
							{curlShown ? <EyeOffIcon /> : <EyeIcon />}
						</Button>
					</div>
					<CodeBlock
						lineNumbers={false}
						wrap
						copyText={apiExample(secret)}
						label={curlShown ? undefined : "Curl command, token masked"}
						className={cn("[&_code]:break-all", !curlShown && "select-none")}
					>
						{apiExample(curlShown ? secret : "***")}
					</CodeBlock>
				</div>
			</SheetBody>
			<SheetFooter>
				<Button onClick={onDone}>Done</Button>
			</SheetFooter>
		</>
	);
}
