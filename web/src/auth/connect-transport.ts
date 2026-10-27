import { isMessage } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { WhoAmIResponseSchema } from "@gen/loco/user/v1/user_pb";
import { PUBLIC_APP_ENV } from "loco:env";
import { nonEmpty } from "@/lib/utils";

import { authClient, BASE_URL } from "./client";
import { setClaimedOrgs } from "./claimed";

const APP_ENV = nonEmpty(PUBLIC_APP_ENV, "DEVELOPMENT");

export const createTransport = (baseUrl: string = BASE_URL) => {
	return createConnectTransport({
		baseUrl,
		useBinaryFormat: APP_ENV === "PRODUCTION",
		interceptors: [
			(next) => async (req) => {
				const res = await next(req);
				if (res.stream || !isMessage(res.message, WhoAmIResponseSchema) || res.message.claimedOrgs.length === 0) {
					return res;
				}
				setClaimedOrgs(res.message.claimedOrgs);
				res.message.claimedOrgs = [];
				return res;
			},
			(next) => async (req) => {
				if (req.url.includes("/ConfigService/")) return await next(req);
				const client = await authClient();
				const token = await client.getAccessToken();
				if (token !== null) req.header.set("Authorization", `Bearer ${token}`);
				try {
					return await next(req);
				} catch (err) {
					if (!(err instanceof ConnectError) || err.code !== Code.Unauthenticated || token === null) throw err;
					const fresh = await client.getAccessToken(true);
					if (fresh === null) throw err;
					req.header.set("Authorization", `Bearer ${fresh}`);
					return await next(req);
				}
			},
		],
	});
};

export const transport = createTransport();
