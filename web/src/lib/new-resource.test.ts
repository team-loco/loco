import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { PlatformDomainSchema } from "@gen/loco/domain/v1/domain_pb";

import {
	errorCount,
	locoYaml,
	nameFromImage,
	parseEnvText,
	servedDomains,
	validateForm,
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
	envText: "LOG_LEVEL=info\nexport ON='yes: really'",
	healthPath: "/healthz",
};

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

test("parseEnvText reports lines the API would reject", () => {
	expect(parseEnvText("A=1\n# note\n\nA=2")).toEqual({ vars: [["A", "2"]], error: null });
	expect(parseEnvText("A=1\n1BAD=x").error).toBe("Line 2 is not KEY=value with a letter or _ first");
	expect(parseEnvText("EMPTY=").error).toBe("EMPTY has no value");
});

test("validateForm accepts a complete form", () => {
	expect(errorCount(validateForm(form, new Map(), "available"))).toBe(0);
});

test("validateForm follows the API's name rules", () => {
	const nameErr = (name: string, taken = new Map<string, string>()) =>
		validateForm({ ...form, name }, taken, "available").name;
	expect(nameErr("")).toBe("Enter a name");
	expect(nameErr("!!!")).toBe("Use at least one letter or digit");
	expect(nameErr("Localhost")).toBe("localhost is reserved");
	expect(nameErr("x".repeat(64))).toBe("Use at most 63 characters");
	expect(nameErr("web", new Map([["web", "web"]]))).toBe("web already exists in this workspace");
	expect(nameErr("Web", new Map([["web", "web"]]))).toBe("web already uses the name web");
});

test("validateForm flags every missing or malformed field", () => {
	const errors = validateForm(
		{
			...form,
			image: "Not An Image",
			regions: [],
			cpu: " ",
			memory: "",
			subdomain: "Bad_Sub",
			port: "80a",
			envText: "KEY=",
			healthPath: "healthz",
		},
		new Map(),
		"empty",
	);
	expect(errorCount(errors)).toBe(8);
	expect(validateForm({ ...form, platformDomain: "" }, new Map(), "empty").subdomain).toBe(
		"No platform domain is served in every selected region",
	);
	expect(validateForm(form, new Map(), "taken").subdomain).toBe("web.example.com is taken");
	expect(validateForm({ ...form, isPublic: false, platformDomain: "" }, new Map(), "taken").subdomain).toBeNull();
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
				health: { path: "/healthz" },
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
		form: { ...form, cpu: "1", isPublic: false, envText: "", healthPath: "", regions: ["on"] },
	});
	expect(yaml).toContain('"on": { cpu: "1", memory: 512Mi');
	expect(yaml).not.toContain("domains");
	expect(yaml).not.toContain("health");
	expect(yaml.startsWith("version: 1\n")).toBe(true);
});
