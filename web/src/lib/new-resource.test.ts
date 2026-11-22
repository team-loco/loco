import { expect, test } from "bun:test";
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { BadRequestSchema, ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";
import { ViolationsSchema } from "@gen/buf/validate/validate_pb";
import { PlatformDomainSchema } from "@gen/loco/domain/v1/domain_pb";

import { draftSizing, parseDrafts } from "./drafts";

import {
	apiFieldError,
	envError,
	envRowErrors,
	firstError,
	healthCheckSpec,
	scalingSpec,
	imageError,
	locoYaml,
	commandEntries,
	commandSummary,
	commandText,
	mergeEnv,
	nameFromImage,
	portRangeError,
	servedDomains,
	validateForm,
	type EnvRow,
	type NewResourceForm,
} from "./new-resource";

const form: NewResourceForm = {
	image: "ghcr.io/acme/web:1.4.2",
	name: "Web API",
	slug: "web",
	description: "",
	regions: ["us-va-1", "us-ca-1"],
	cpu: "250m",
	memory: "512Mi",
	replicas: 2,
	maxReplicas: 2,
	cpuTarget: 70,
	isPublic: true,
	subdomain: "web",
	platformDomain: "example.com",
	port: "8080",
	vars: [
		{ id: "1", key: "LOG_LEVEL", value: "info" },
		{ id: "2", key: "ON", value: "yes: really" },
	],
	health: { enabled: true, path: "/healthz", interval: "10", timeout: "3", failAfter: "5" },
	command: "",
	args: "-listen=:8080\n-text=hello, world\n\n",
};

const row = (key: string, value: string, id = key): EnvRow => ({ id, key, value });

test("nameFromImage slugs the repository and skips taken names", () => {
	expect(nameFromImage("ghcr.io/acme/My_Web:1.0", new Set())).toBe("my-web");
	expect(nameFromImage("nginx@sha256:abc", new Set(["nginx", "nginx-2"]))).toBe("nginx-3");
	expect(nameFromImage("", new Set())).toBe("");
});

test("servedDomains keeps only domains every selected region serves", () => {
	const domains = [
		create(PlatformDomainSchema, { domain: "a.test", regions: ["us-va-1", "us-ca-1"] }),
		create(PlatformDomainSchema, { domain: "b.test", regions: ["us-va-1"] }),
	];
	expect(servedDomains(domains, ["us-va-1", "us-ca-1"])).toEqual(["a.test"]);
	expect(servedDomains(domains, ["us-va-1"])).toEqual(["a.test", "b.test"]);
});

test("envRowErrors names every row the API would reject and allows empty values", () => {
	expect(envRowErrors([row("A", "1"), row("", "", "blank"), row("EMPTY", "")]).size).toBe(0);
	expect(envError([row("EMPTY", "")])).toBeNull();
	expect(
		Object.fromEntries(
			envRowErrors([row("", "x", "unnamed"), row("1BAD", "x", "bad"), row("A", "1", "a"), row(" A ", "2", "b")]),
		),
	).toEqual({
		unnamed: "Enter a name for this value",
		bad: "Use letters, digits and _, not starting with a digit",
		a: "A is set more than once",
		b: "A is set more than once",
	});
	expect(envError([row("", "x"), row("1BAD", "x")])).toBe("Fix the 2 highlighted variables");
});

test("mergeEnv replaces pasted keys in place, appends new ones and drops blank rows", () => {
	let next = 0;
	const newId = () => {
		next += 1;
		return `new-${next.toString()}`;
	};
	expect(
		mergeEnv(
			[row("A", "1"), row("", "", "blank"), row("B", "2")],
			[
				["B", "3"],
				["C", "4"],
			],
			newId,
		),
	).toEqual([row("A", "1"), row("B", "3"), { id: "new-1", key: "C", value: "4" }]);
});

test("validateForm accepts a complete form", () => {
	expect(firstError(validateForm(form, "available", 10))).toBeNull();
});

test("portRangeError accepts 1 through 65535 and rejects the rest", () => {
	for (const ok of ["1", "8080", "65535", " 443 "]) expect(portRangeError(ok)).toBeNull();
	for (const bad of ["", "0", "65536", "70000", "-1", "80.5", "http", "1e3"])
		expect(portRangeError(bad)).toBe("Use a port from 1 to 65535");
});

test("validateForm rejects a port outside 1 through 65535", () => {
	expect(validateForm({ ...form, port: "70000" }, "available", 10).port).toBe("Use a port from 1 to 65535");
	expect(validateForm({ ...form, port: "0" }, "available", 10).port).toBe("Use a port from 1 to 65535");
	expect(firstError(validateForm({ ...form, port: "0" }, "available", 10))).toBe("Use a port from 1 to 65535");
});

test("validateForm leaves health check numbers to the API", () => {
	const loose = { ...form, health: { enabled: true, path: "healthz", interval: "", timeout: "0", failAfter: "" } };
	expect(firstError(validateForm(loose, "available", 10))).toBeNull();
});

test("validateForm needs a path for a health check that is on", () => {
	const empty = { ...form, health: { ...form.health, path: " " } };
	expect(validateForm(empty, "available", 10).healthPath).toBe("Needs a path");
	expect(firstError(validateForm(empty, "available", 10))).toBe("Needs a path");
	expect(
		validateForm({ ...empty, health: { ...empty.health, enabled: false } }, "available", 10).healthPath,
	).toBeNull();
});

test("validateForm rejects a health check timeout that is not below the interval", () => {
	const health = (interval: string, timeout: string) => ({
		...form,
		health: { enabled: true, path: "/healthz", interval, timeout, failAfter: "3" },
	});
	expect(validateForm(health("10", "10"), "available", 10).healthTimeout).toBe("Must be below the interval");
	expect(validateForm(health("5", "9"), "available", 10).healthTimeout).toBe("Must be below the interval");
	expect(firstError(validateForm(health("5", "9"), "available", 10))).toBe("Must be below the interval");
	expect(validateForm(health("10", "9"), "available", 10).healthTimeout).toBeNull();
	expect(validateForm(health("0", "4"), "available", 10).healthTimeout).toBeNull();
});

test("validateForm accepts an empty port only for a private service without a health check", () => {
	const noHealth = { ...form.health, enabled: false };
	const worker = { ...form, port: "", isPublic: false, health: noHealth };
	expect(validateForm(worker, "available", 10).port).toBeNull();
	expect(validateForm({ ...worker, isPublic: true }, "available", 10).port).toBe("A public service needs a port");
	expect(validateForm({ ...worker, health: form.health }, "available", 10).port).toBe("A health check needs a port");
});

test("firstError reports the first blocking field in form order", () => {
	const errors = validateForm({ ...form, regions: [], subdomain: "Bad_Sub", vars: [row("", "x")] }, "empty", 10);
	expect([errors.regions, errors.subdomain, errors.env].every((e) => e !== null)).toBe(true);
	expect(firstError(errors)).toBe("Choose at least one region");
	expect(validateForm({ ...form, platformDomain: "" }, "empty", 10).subdomain).toBe(
		"No platform domain is served in every selected region",
	);
	expect(validateForm(form, "taken", 10).subdomain).toBe("web.example.com is taken");
	expect(validateForm({ ...form, isPublic: false, platformDomain: "" }, "taken", 10).subdomain).toBeNull();
});

test("healthCheckSpec sends the fields the user set, or no check when off or without a path", () => {
	expect(healthCheckSpec(form.health, 4)).toEqual({
		path: "/healthz",
		intervalSeconds: 10,
		timeoutSeconds: 3,
		failureThreshold: 5,
		initialDelaySeconds: 4,
	});
	expect(healthCheckSpec({ ...form.health, path: "" }, 4)).toBeUndefined();
	expect(healthCheckSpec({ ...form.health, enabled: false }, 4)).toBeUndefined();
});

test("apiFieldError maps a validation violation to the field it names", () => {
	const violations = create(ViolationsSchema, {
		violations: [
			{
				message: "must be greater than or equal to 1",
				field: {
					elements: [
						{ fieldName: "spec" },
						{ fieldName: "service" },
						{ fieldName: "health_check" },
						{ fieldName: "interval_seconds" },
					],
				},
			},
		],
	});
	const err = new ConnectError("validation error", Code.InvalidArgument, undefined, [
		{ desc: ViolationsSchema, value: violations },
	]);
	expect(apiFieldError(err)).toEqual({ field: "interval", message: "Must be greater than or equal to 1" });
	expect(apiFieldError(new ConnectError("name is taken", Code.AlreadyExists))).toBeNull();
	expect(apiFieldError(new Error("offline"))).toBeNull();
});

test("locoYaml writes a loco.yaml that parses to the form", () => {
	const yaml = locoYaml({ schemaUrl: "https://loco.test/schemas/loco.v1.json", form });
	expect(yaml.split("\n")[0]).toBe("# yaml-language-server: $schema=https://loco.test/schemas/loco.v1.json");
	expect(Bun.YAML.parse(yaml)).toEqual({
		version: 1,
		partial: "web",
		services: {
			web: {
				image: "ghcr.io/acme/web:1.4.2",
				args: ["-listen=:8080", "-text=hello, world"],
				port: 8080,
				health: { path: "/healthz", interval: 10, timeout: 3, failThreshold: 5 },
				domains: ["web.example.com"],
				env: { LOG_LEVEL: "info", ON: "yes: really" },
				regions: {
					"us-va-1": { cpu: "250m", memory: "512Mi", replicas: { min: 2, max: 2 } },
					"us-ca-1": { cpu: "250m", memory: "512Mi", replicas: { min: 2, max: 2 } },
				},
			},
		},
	});
});

test("commandSummary joins the entries or names the image default", () => {
	expect(commandSummary({ command: "", args: "" })).toBe("Image default");
	expect(commandSummary({ command: "/http-echo", args: "-text=hi\n" })).toBe("/http-echo -text=hi");
});

test("commandEntries keeps one entry per line as typed", () => {
	expect(commandEntries("")).toEqual([]);
	expect(commandEntries("/bin/sh\r\n-c\n\n  \nexec app --name 'a b'\n")).toEqual([
		"/bin/sh",
		"-c",
		"exec app --name 'a b'",
	]);
	expect(commandEntries(" -v ")).toEqual([" -v "]);
	expect(commandEntries(commandText(["-text=a, b", "$(PORT)"]))).toEqual(["-text=a, b", "$(PORT)"]);
});

test("locoYaml omits domains until the subdomain and platform domain are known", () => {
	const yaml = (patch: Partial<typeof form>) =>
		Bun.YAML.parse(locoYaml({ schemaUrl: "", form: { ...form, isPublic: true, ...patch } })) as {
			services: Record<string, { domains?: string[] }>;
		};
	const domains = (patch: Partial<typeof form>) => Object.values(yaml(patch).services)[0]?.domains;
	expect(domains({ subdomain: "web", platformDomain: "" })).toBeUndefined();
	expect(domains({ subdomain: "", platformDomain: "loco.test" })).toBeUndefined();
	expect(domains({ subdomain: "web", platformDomain: "loco.test" })).toEqual(["web.loco.test"]);
});

test("locoYaml writes the command", () => {
	const yaml = locoYaml({ schemaUrl: "", form: { ...form, command: "/bin/sh\n-c", args: "exec app: now" } });
	expect(yaml).toContain('    command: [/bin/sh, -c]\n    args: ["exec app: now"]\n');
});

test("locoYaml quotes values YAML would read as numbers or booleans", () => {
	const yaml = locoYaml({
		schemaUrl: "",
		form: { ...form, cpu: "1", isPublic: false, vars: [], health: { ...form.health, path: "" }, regions: ["on"] },
	});
	expect(yaml).toContain('"on": { cpu: "1", memory: 512Mi');
	expect(yaml).not.toContain("domains");
	expect(yaml).not.toContain("health");
	expect(yaml.startsWith("version: 1\n")).toBe(true);
	expect(
		locoYaml({
			schemaUrl: "",
			form: { ...form, health: { enabled: true, path: "/up", interval: "", timeout: "", failAfter: "2" } },
		}),
	).toContain("    health: { path: /up, failThreshold: 2 }\n");
});

test("imageError asks for an image reference before the drawer opens", () => {
	expect(imageError("  ")).toBe("Enter an image");
	expect(imageError("Not An Image")).toBe("An image reference can't contain spaces");
	expect(imageError("UPPER/Case")).toBe("Use lowercase letters in the image reference");
	expect(imageError("nope::bad")).toBe("Use registry/repository:tag");
	expect(imageError(" ghcr.io/acme/web:1.4.2 ")).toBeNull();
});

test("the YAML keeps a secret reference as the variable's value", () => {
	const vars = [row("LOG_LEVEL", "info"), row("API_KEY", "${{ secrets.API_KEY }}")];
	const yaml = locoYaml({ schemaUrl: "", form: { ...form, vars } });
	expect(yaml).toContain('LOG_LEVEL: "info"');
	expect(yaml).toContain('API_KEY: "${{ secrets.API_KEY }}"');
});

test("apiFieldError decodes image resolution details", () => {
	const message = 'Image "nginx:missing" was not found. Check the repository and tag or digest.';
	const violations = create(BadRequestSchema, {
		fieldViolations: [
			{ field: "unknown", description: "skip this field" },
			{ field: "image", description: message },
		],
	});
	const info = create(ErrorInfoSchema, { reason: "IMAGE_NOT_FOUND", domain: "loco.build" });
	const err = new ConnectError(message, Code.InvalidArgument);
	err.details.push(
		{ type: BadRequestSchema.typeName, value: toBinary(BadRequestSchema, violations) },
		{ type: ErrorInfoSchema.typeName, value: toBinary(ErrorInfoSchema, info) },
	);
	expect(apiFieldError(err)).toEqual({ field: "image", message });
});

test("apiFieldError maps nested BadRequest fields with the same names as validation fields", () => {
	const err = new ConnectError("invalid interval", Code.InvalidArgument, undefined, [
		{
			desc: BadRequestSchema,
			value: create(BadRequestSchema, {
				fieldViolations: [{ field: "spec.service.health_check.interval_seconds", description: "must be positive" }],
			}),
		},
	]);
	expect(apiFieldError(err)).toEqual({ field: "interval", message: "Must be positive" });
});

test("form scaling sends fixed replicas or CPU autoscaling", () => {
	expect(scalingSpec(form)).toEqual({
		minReplicas: 2,
		maxReplicas: 2,
		scalers: { enabled: false },
	});
	const autoscaled = { ...form, maxReplicas: 5 };
	expect(firstError(validateForm(autoscaled, "available", 10))).toBeNull();
	expect(scalingSpec(autoscaled)).toEqual({
		minReplicas: 2,
		maxReplicas: 5,
		scalers: { enabled: true, cpuTarget: 70 },
	});
	const yaml = Bun.YAML.parse(locoYaml({ schemaUrl: "", form: autoscaled }));
	expect(yaml).toMatchObject({
		services: { web: { regions: { "us-va-1": { replicas: { min: 2, max: 5 }, autoscaling: { cpuTarget: 70 } } } } },
	});
	for (const patch of [{ replicas: 0 }, { maxReplicas: 1 }, { cpuTarget: 0 }, { cpuTarget: 101 }, { cpuTarget: 1.5 }]) {
		expect(validateForm({ ...autoscaled, ...patch }, "available", 10).scaling).not.toBeNull();
	}
});

test("saved autoscaling drafts reload and serialize the same targets", () => {
	const saved = [
		{
			id: "saved",
			name: "web",
			slug: "web",
			image: form.image,
			createdMs: 1,
			edits: { replicas: 1, maxReplicas: 3, cpuTarget: 65 },
		},
	];
	const [draft] = parseDrafts(JSON.parse(JSON.stringify(saved)));
	if (draft === undefined) throw new Error("draft missing");
	const loaded = {
		...form,
		...draftSizing(draft, { cpu: form.cpu, memory: form.memory, minReplicas: 1, cpuTarget: form.cpuTarget }),
	};
	expect(firstError(validateForm(loaded, "available", 10))).toBeNull();
	expect(scalingSpec(loaded)).toMatchObject({
		minReplicas: 1,
		maxReplicas: 3,
		scalers: { enabled: true, cpuTarget: 65 },
	});
});

test("draft validation follows the replica limit returned by the API", () => {
	expect(validateForm({ ...form, maxReplicas: 4 }, "available", 3).scaling).toBe("Use at most 3 replicas per region");
	expect(validateForm({ ...form, maxReplicas: 4 }, "available", 4).scaling).toBeNull();
});
