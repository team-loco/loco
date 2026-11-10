import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { ViolationsSchema } from "@gen/buf/validate/validate_pb";
import { PlatformDomainSchema } from "@gen/loco/domain/v1/domain_pb";

import {
	apiFieldError,
	deploymentVariables,
	envError,
	firstError,
	healthCheckSpec,
	healthSummary,
	imageError,
	looksSecret,
	locoYaml,
	mergeEnv,
	nameError,
	nameFromImage,
	servedDomains,
	validateForm,
	type EnvRow,
	type NewResourceForm,
} from "./new-resource";

const form: NewResourceForm = {
	image: "ghcr.io/acme/web:1.4.2",
	name: "web",
	regions: ["us-east-1", "eu-west-1"],
	cpu: "250m",
	memory: "512Mi",
	replicas: 2,
	isPublic: true,
	subdomain: "web",
	platformDomain: "example.com",
	port: "8080",
	vars: [
		{ id: "1", key: "LOG_LEVEL", value: "info" },
		{ id: "2", key: "ON", value: "yes: really" },
	],
	health: { path: "/healthz", interval: "10", timeout: "3", failAfter: "5" },
};

const row = (key: string, value: string, id = key): EnvRow => ({ id, key, value });

test("nameFromImage slugs the repository and skips taken names", () => {
	expect(nameFromImage("ghcr.io/acme/My_Web:1.0", new Set())).toBe("my-web");
	expect(nameFromImage("nginx@sha256:abc", new Set(["nginx", "nginx-2"]))).toBe("nginx-3");
	expect(nameFromImage("", new Set())).toBe("");
});

test("servedDomains keeps only domains every selected region serves", () => {
	const domains = [
		create(PlatformDomainSchema, { domain: "a.test", regions: ["us-east-1", "eu-west-1"] }),
		create(PlatformDomainSchema, { domain: "b.test", regions: ["us-east-1"] }),
	];
	expect(servedDomains(domains, ["us-east-1", "eu-west-1"])).toEqual(["a.test"]);
	expect(servedDomains(domains, ["us-east-1"])).toEqual(["a.test", "b.test"]);
});

test("envError ignores blank rows and reports rows the API would reject", () => {
	expect(envError([row("A", "1"), row("", "", "blank")])).toBeNull();
	expect(envError([row("", "x")])).toBe("A variable has a value but no name");
	expect(envError([row("1BAD", "x")])).toBe(
		"1BAD isn't a valid name: use letters, digits and _, not starting with a digit",
	);
	expect(envError([row("EMPTY", "")])).toBe("EMPTY has no value");
	expect(envError([row("A", "1", "a"), row(" A ", "2", "b")])).toBe("A is set twice");
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
	expect(firstError(validateForm(form, "available"))).toBeNull();
});

test("nameError follows the API's name rules", () => {
	const nameErr = (name: string, taken = new Map<string, string>()) => nameError(name, taken);
	expect(nameErr("")).toBe("Enter a name");
	expect(nameErr("!!!")).toBe("Use at least one letter or digit");
	expect(nameErr("Localhost")).toBe("localhost is reserved");
	expect(nameErr("x".repeat(64))).toBe("Use at most 63 characters");
	expect(nameErr("web", new Map([["web", "web"]]))).toBe("web already exists in this workspace");
	expect(nameErr("Web", new Map([["web", "web"]]))).toBe("web already uses the name web");
});

test("validateForm leaves port and health check numbers to the API", () => {
	const loose = { ...form, port: "", health: { path: "healthz", interval: "", timeout: "0", failAfter: "" } };
	expect(firstError(validateForm(loose, "available"))).toBeNull();
});

test("firstError reports the first blocking field in form order", () => {
	const errors = validateForm(
		{ ...form, regions: [], cpu: " ", memory: "", subdomain: "Bad_Sub", vars: [row("KEY", "")] },
		"empty",
	);
	expect([errors.regions, errors.subdomain, errors.cpu, errors.memory, errors.env].every((e) => e !== null)).toBe(true);
	expect(firstError(errors)).toBe("Choose at least one region");
	expect(validateForm({ ...form, platformDomain: "" }, "empty").subdomain).toBe(
		"No platform domain is served in every selected region",
	);
	expect(validateForm(form, "taken").subdomain).toBe("web.example.com is taken");
	expect(validateForm({ ...form, isPublic: false, platformDomain: "" }, "taken").subdomain).toBeNull();
});

test("healthCheckSpec sends the fields the user set, or no check without a path", () => {
	expect(healthCheckSpec(form.health, 4)).toEqual({
		path: "/healthz",
		intervalSeconds: 10,
		timeoutSeconds: 3,
		failureThreshold: 5,
		initialDelaySeconds: 4,
	});
	expect(healthCheckSpec({ ...form.health, path: "" }, 4)).toBeUndefined();
	expect(healthSummary(form.health)).toBe("/healthz · every 10s");
	expect(healthSummary({ ...form.health, path: "" })).toBe("None");
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
				port: 8080,
				health: { path: "/healthz", interval: 10, timeout: 3, failThreshold: 5 },
				domains: ["web.example.com"],
				env: { LOG_LEVEL: "info", ON: "yes: really" },
				regions: {
					"us-east-1": { cpu: "250m", memory: "512Mi", replicas: { min: 2, max: 2 } },
					"eu-west-1": { cpu: "250m", memory: "512Mi", replicas: { min: 2, max: 2 } },
				},
			},
		},
	});
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
		locoYaml({ schemaUrl: "", form: { ...form, health: { path: "/up", interval: "", timeout: "", failAfter: "2" } } }),
	).toContain("    health: { path: /up, failThreshold: 2 }\n");
});

test("looksSecret flags keys whose words name a credential", () => {
	expect(
		["API_KEY", "stripe_secret_key", "GITHUB_TOKEN", "DB_PASSWORD", "DATABASE_URL", "SENTRY_DSN"].map(looksSecret),
	).toEqual([true, true, true, true, true, true]);
	expect(["LOG_LEVEL", "PORT", "KEYBOARD_LAYOUT", "PUBLIC_URL", "MONKEY"].map(looksSecret)).toEqual([
		false,
		false,
		false,
		false,
		false,
	]);
});

test("imageError asks for an image reference before the drawer opens", () => {
	expect(imageError("  ")).toBe("Enter an image");
	expect(imageError("Not An Image")).toBe("Use registry/repository:tag");
	expect(imageError(" ghcr.io/acme/web:1.4.2 ")).toBeNull();
});

test("marked values only go to the secret write and the YAML names their bindings", () => {
	const vars = [row("LOG_LEVEL", "info"), { ...row("API_KEY", "private-test-value"), secret: true }];
	expect(deploymentVariables(vars)).toEqual({ env: { LOG_LEVEL: "info" }, secrets: { API_KEY: "private-test-value" } });
	expect(envError([{ ...row("api_key", "value"), secret: true }])).toContain("must use uppercase");
	const yaml = locoYaml({ schemaUrl: "", form: { ...form, vars } });
	expect(yaml).toContain("secrets: [API_KEY]");
	expect(yaml).toContain('LOG_LEVEL: "info"');
	expect(yaml).not.toContain("private-test-value");
});
